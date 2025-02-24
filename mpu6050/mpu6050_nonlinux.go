//go:build !linux
// +build !linux

package mpu6050

import (
	"context"
	"errors"
	
	"go.viam.com/rdk/components/movementsensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)


func NewMpu6050(
	ctx context.Context,
	logger logging.Logger,
	name string,
	busName string,
	useAlternateI2CAddress bool,
) (movementsensor.MovementSensor, error) {
	return nil, errors.New("mpu6050 only supported on linux")
}

func newMpu6050(
	ctx context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (movementsensor.MovementSensor, error) {
	return nil, errors.New("mpu6050 only supported on linux")
}
