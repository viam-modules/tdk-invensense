//go:build !linux

package mpu6050

import (
	"context"
	"errors"

	"go.viam.com/rdk/components/movementsensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

var errUnsupportedPlatform = errors.New("mpu6050 only supported on linux")

// NewMpu6050 always fails on non-Linux platforms, which have no I2C support.
func NewMpu6050(
	_ context.Context,
	_ logging.Logger,
	_ string,
	_ string,
	_ bool,
) (movementsensor.MovementSensor, error) {
	return nil, errUnsupportedPlatform
}

func newMpu6050(
	_ context.Context,
	_ resource.Dependencies,
	_ resource.Config,
	_ logging.Logger,
) (movementsensor.MovementSensor, error) {
	return nil, errUnsupportedPlatform
}
