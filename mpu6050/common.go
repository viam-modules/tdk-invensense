// Package mpu6050 is only implemented for Linux systems.
package mpu6050

import (
	"go.viam.com/rdk/components/movementsensor"
	"go.viam.com/rdk/resource"
)

// Model for viam supported tdk-invensense mpu6050 movement sensor.
var Model = resource.NewModel("viam", "tdk-invensense", "mpu6050")

// Config is used to configure the attributes of the chip.
type Config struct {
	I2cBus                 string `json:"i2c_bus"`
	UseAlternateI2CAddress bool   `json:"use_alt_i2c_address,omitempty"`
}

// Validate ensures all parts of the config are valid, and then returns the list of things we
// depend on.
func (conf *Config) Validate(path string) ([]string, error) {
	if conf.I2cBus == "" {
		return nil, resource.NewConfigValidationFieldRequiredError(path, "i2c_bus")
	}

	var deps []string
	return deps, nil
}

func init() {
	resource.RegisterComponent(movementsensor.API, Model, resource.Registration[movementsensor.MovementSensor, *Config]{
		Constructor: newMpu6050,
	})
}
