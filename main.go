// Package main is a Viam module providing the tdk-invensense mpu6050 movement sensor.
package main

import (
	"go.viam.com/rdk/components/movementsensor"
	"go.viam.com/rdk/module"
	"go.viam.com/rdk/resource"

	"github.com/viam-modules/tdk-invensense/mpu6050"
)

func main() {
	module.ModularMain(resource.APIModel{API: movementsensor.API, Model: mpu6050.Model})
}
