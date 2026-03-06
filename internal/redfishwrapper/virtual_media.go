package redfishwrapper

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/stmcginnis/gofish/schemas"
)

// getVirtualMedia retrieves virtual media resources by first checking the
// Redfish Manager path and falling back to the System path if none are found.
//
// Some BMC implementations (e.g., Dell iDRAC) expose VirtualMedia under the
// System resource (/redfish/v1/Systems/{SystemId}/VirtualMedia) rather than
// the Manager resource (/redfish/v1/Managers/{ManagerId}/VirtualMedia).
// Both locations are valid per the Redfish specification.
func (c *Client) getVirtualMedia(ctx context.Context) ([]*schemas.VirtualMedia, error) {
	// Try Manager path first (standard Redfish location).
	m, err := c.Manager(ctx)
	if err == nil {
		vm, err := m.VirtualMedia()
		if err == nil && len(vm) > 0 {
			return vm, nil
		}
	}

	// Fallback to System path (Dell iDRAC and other implementations that
	// expose VirtualMedia under ComputerSystem per Redfish spec v1.12.0+).
	sys, err := c.System()
	if err == nil {
		vm, err := sys.VirtualMedia()
		if err == nil && len(vm) > 0 {
			return vm, nil
		}
	}

	return nil, errors.New("no virtual media found at Manager or System resource paths")
}

// SetVirtualMedia sets the virtual media attached to the system, or ejects
// all matching media if mediaURL is empty.
//
// When multiple VirtualMedia members match the requested media type, each
// slot is tried in order. If an operation (eject, insert) fails on one slot,
// the error is recorded and the next matching slot is attempted. This handles
// BMCs that expose non-functional VirtualMedia slots (e.g., Dell iDRAC Remote
// File Share slots that advertise the same MediaTypes as the bootable virtual
// optical drive but may reject InsertMedia).
func (c *Client) SetVirtualMedia(ctx context.Context, kind string, mediaURL string) (bool, error) {
	var mediaKind schemas.VirtualMediaType

	switch kind {
	case "CD":
		mediaKind = schemas.CDVirtualMediaType
	case "Floppy":
		mediaKind = schemas.FloppyVirtualMediaType
	case "USBStick":
		mediaKind = schemas.USBStickVirtualMediaType
	case "DVD":
		mediaKind = schemas.DVDVirtualMediaType
	default:
		return false, errors.New("invalid media type")
	}

	virtualMedia, err := c.getVirtualMedia(ctx)
	if err != nil {
		return false, err
	}

	supportedMediaTypes := []string{}
	var slotErrors []error

	for _, vm := range virtualMedia {
		if !slices.Contains(vm.MediaTypes, mediaKind) {
			for _, mt := range vm.MediaTypes {
				supportedMediaTypes = append(supportedMediaTypes, string(mt))
			}

			continue
		}

		if mediaURL == "" {
			// Only ejecting the media was requested.
			if vm.Inserted != nil && *vm.Inserted && vm.SupportsMediaEject {
				if _, err := vm.EjectMedia(); err != nil {
					slotErrors = append(slotErrors, fmt.Errorf("%s: eject: %w", vm.ODataID, err))
					continue
				}
			}

			return true, nil
		}

		// Ejecting the media before inserting new media makes the success rate of inserting the new media higher.
		if vm.Inserted != nil && *vm.Inserted && vm.SupportsMediaEject {
			if _, err := vm.EjectMedia(); err != nil {
				slotErrors = append(slotErrors, fmt.Errorf("%s: eject before insert: %w", vm.ODataID, err))
				continue
			}
		}

		if !vm.SupportsMediaInsert {
			slotErrors = append(slotErrors, fmt.Errorf("%s: does not support insert", vm.ODataID))
			continue
		}

		inserted := true
		writeProtected := true
		params := schemas.VirtualMediaInsertMediaParameters{
			Image:          mediaURL,
			Inserted:       &inserted,
			WriteProtected: &writeProtected,
		}
		if _, err := vm.InsertMedia(&params); err != nil {
			// Some BMCs (e.g., Supermicro X11SDV-4C-TLN2F) don't support the
			// Inserted and WriteProtected properties, so retry without them.
			paramsMinimal := schemas.VirtualMediaInsertMediaParameters{Image: mediaURL}
			if _, err := vm.InsertMedia(&paramsMinimal); err != nil {
				slotErrors = append(slotErrors, fmt.Errorf("%s: insert: %w", vm.ODataID, err))
				continue
			}
		}

		return true, nil
	}

	if len(slotErrors) > 0 {
		return false, fmt.Errorf("all matching virtual media slots failed: %w", errors.Join(slotErrors...))
	}

	return false, fmt.Errorf("not a supported media type: %s. supported media types: %v", kind, supportedMediaTypes)
}

func (c *Client) InsertedVirtualMedia(ctx context.Context) ([]string, error) {
	virtualMedia, err := c.getVirtualMedia(ctx)
	if err != nil {
		return nil, err
	}

	var inserted []string

	for _, media := range virtualMedia {
		if media.Inserted != nil && *media.Inserted {
			inserted = append(inserted, media.ID)
		}
	}

	return inserted, nil
}
