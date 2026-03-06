package redfishwrapper

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/stmcginnis/gofish/schemas"
)

// getVirtualMedia retrieves virtual media resources by first checking the
// Redfish Manager path and falling back to the System path if none are found.
//
// Some BMC implementations (e.g., Dell iDRAC) expose VirtualMedia under the
// System resource (/redfish/v1/Systems/{SystemId}/VirtualMedia) rather than
// the Manager resource (/redfish/v1/Managers/{ManagerId}/VirtualMedia).
// Both locations are valid per the Redfish specification.
//
// The returned slice is sorted by ODataID for deterministic slot ordering.
// gofish fetches collection members concurrently, returning them in arbitrary
// order. Without sorting, the wrong slot may be selected on multi-slot BMCs
// (e.g., Dell iDRAC10 VirtualMedia/2 before VirtualMedia/1).
func (c *Client) getVirtualMedia(ctx context.Context) ([]*schemas.VirtualMedia, error) {
	var vm []*schemas.VirtualMedia

	// Try Manager path first (standard Redfish location).
	if m, err := c.Manager(ctx); err == nil {
		if v, err := m.VirtualMedia(); err == nil && len(v) > 0 {
			vm = v
		}
	}

	// Fallback to System path (Dell iDRAC and other implementations that
	// expose VirtualMedia under ComputerSystem per Redfish spec v1.12.0+).
	if len(vm) == 0 {
		if sys, err := c.System(); err == nil {
			if v, err := sys.VirtualMedia(); err == nil && len(v) > 0 {
				vm = v
			}
		}
	}

	if len(vm) == 0 {
		return nil, errors.New("no virtual media found at Manager or System resource paths")
	}

	// Sort once at the single exit point for deterministic slot ordering.
	sort.Slice(vm, func(i, j int) bool { return vm[i].ODataID < vm[j].ODataID })

	return vm, nil
}

// Set the virtual media attached to the system, or just eject everything if mediaURL is empty.
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
					return false, fmt.Errorf("error ejecting media: %v", err)
				}
			}

			return true, nil
		}

		// Ejecting the media before inserting a new new media makes the success rate of inserting the new media higher.
		if vm.Inserted != nil && *vm.Inserted && vm.SupportsMediaEject {
			if _, err := vm.EjectMedia(); err != nil {
				return false, fmt.Errorf("error ejecting media before inserting media: %v", err)
			}
		}

		if !vm.SupportsMediaInsert {
			return false, fmt.Errorf("BMC does not support inserting virtual media of kind: %s", kind)
		}

		inserted := true
		writeProtected := true
		params := schemas.VirtualMediaInsertMediaParameters{
			Image:          mediaURL,
			Inserted:       &inserted,
			WriteProtected: &writeProtected,
		}
		if _, err := vm.InsertMedia(&params); err != nil {
			// Some BMC's (Supermicro X11SDV-4C-TLN2F, for example) don't support the "inserted" and "writeProtected" properties,
			// so we try to insert the media without them if the first attempt fails.
			paramsMinimal := schemas.VirtualMediaInsertMediaParameters{Image: mediaURL}
			if _, err := vm.InsertMedia(&paramsMinimal); err != nil {
				return false, err
			}
		}

		return true, nil
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
