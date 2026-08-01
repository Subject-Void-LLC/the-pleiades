package inventory

import (
	"fmt"
)

// ItemFactory defines the constructor for hydrating raw database records into
// strongly typed InventoryItem objects.
type ItemFactory struct {
	registry map[string]func(data map[string]interface{}) (InventoryItem, error)
}

// NewItemFactory initializes an empty factory.
func NewItemFactory() *ItemFactory {
	return &ItemFactory{
		registry: make(map[string]func(map[string]interface{}) (InventoryItem, error)),
	}
}

// Register maps a device type string to its constructor function.
func (f *ItemFactory) Register(deviceType string, constructor func(map[string]interface{}) (InventoryItem, error)) {
	f.registry[deviceType] = constructor
}

// Build hydrates a raw map into a concrete InventoryItem based on its type field.
func (f *ItemFactory) Build(data map[string]interface{}) (InventoryItem, error) {
	deviceType, ok := data["type"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid type field in device payload")
	}

	constructor, exists := f.registry[deviceType]
	if !exists {
		return nil, fmt.Errorf("unsupported device type: %s", deviceType)
	}

	return constructor(data)
}
