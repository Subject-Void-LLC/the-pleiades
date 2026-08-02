package inventory

import (
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
)

// ItemFactory defines the constructor for hydrating raw database records into
// strongly typed InventoryItem objects.
type ItemFactory struct {
	registry map[string]func(dev *ent.Device) (InventoryItem, error)
}

// NewItemFactory initializes a factory with standard device types.
func NewItemFactory() *ItemFactory {
	f := &ItemFactory{
		registry: make(map[string]func(*ent.Device) (InventoryItem, error)),
	}

	f.Register("cisco_router", func(dev *ent.Device) (InventoryItem, error) {
		return &CiscoRouter{
			baseDevice: baseDevice{
				id:    dev.Name,
				props: dev.Properties,
				tags:  []string{}, // Note: implement tags if added to ent schema
			},
		}, nil
	})

	f.Register("linux_server", func(dev *ent.Device) (InventoryItem, error) {
		return &LinuxServer{
			baseDevice: baseDevice{
				id:    dev.Name,
				props: dev.Properties,
				tags:  []string{},
			},
		}, nil
	})

	return f
}

// Register maps a device type string to its constructor function.
func (f *ItemFactory) Register(deviceType string, constructor func(*ent.Device) (InventoryItem, error)) {
	f.registry[deviceType] = constructor
}

// Build hydrates an ent.Device into a concrete InventoryItem based on its type field.
func (f *ItemFactory) Build(dev *ent.Device) (InventoryItem, error) {
	if dev.Properties == nil {
		return nil, fmt.Errorf("device properties cannot be nil")
	}

	deviceType, ok := dev.Properties["type"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid type field in device properties")
	}

	constructor, exists := f.registry[deviceType]
	if !exists {
		return nil, fmt.Errorf("unsupported device type: %s", deviceType)
	}

	return constructor(dev)
}
