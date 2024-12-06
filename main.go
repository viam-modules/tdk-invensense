// package main is a module with raspberry pi board component.
package main

import (
	"context"

	"tdk-invensense/mpu6050"
	"tdk-invensense/mpu9250"

	"go.viam.com/rdk/components/movementsensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/module"
	"go.viam.com/utils"
)

func main() {
	utils.ContextualMain(mainWithArgs, module.NewLoggerFromArgs("tdk-invensense"))
}

func mainWithArgs(ctx context.Context, args []string, logger logging.Logger) error {
	module, err := module.NewModuleFromArgs(ctx)
	if err != nil {
		return err
	}

	if err = module.AddModelFromRegistry(ctx, movementsensor.API, mpu6050.Model); err != nil {
		return err
	}

	if err = module.AddModelFromRegistry(ctx, movementsensor.API, mpu9250.Model); err != nil {
		return err
	}

	err = module.Start(ctx)
	defer module.Close(ctx)
	if err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}
