// Package main for testing mpu6050 locally
package main

import (
	"context"
	"time"
	
	"go.viam.com/rdk/logging"

	"github.com/viam-modules/tdk-invensense/mpu6050"
)

func main() {
	err := realMain()
	if err != nil {
		panic(err)
	}
}

func realMain() error {
	ctx := context.Background()
	logger := logging.NewLogger("mpu6050-local")

	ms, err := mpu6050.NewMpu6050(ctx, logger, "foo", "1", false)
	if err != nil {
		return err
	}

	for range 30 {
		av, err := ms.AngularVelocity(ctx, nil)
		if err != nil {
			return err
		}
		la, err := ms.LinearAcceleration(ctx, nil)
		if err != nil {
			return err
		}

		logger.Infof("angular velocity: %0.2f %0.2f %0.2f linear acceleration: %0.2f %0.2f %0.2f", av.X, av.Y, av.Z, la.X, la.Y, la.Z)
		time.Sleep(time.Second)
	}
	return nil
}
