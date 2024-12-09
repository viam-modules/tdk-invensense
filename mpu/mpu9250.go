//go:build linux

// Package mpu implements the movementsensor interface for an MPU-9250 6-axis accelerometer. A
// datasheet for this chip is at
// https://invensense.tdk.com/wp-content/uploads/2015/02/PS-MPU-9250A-01-v1.1.pdf and a
// description of the I2C registers is at
// https://invensense.tdk.com/wp-content/uploads/2015/02/RM-MPU-9250A-00-v1.6.pdf
//
// We support reading the accelerometer, gyroscope, and thermometer data off of the chip. We do not
// yet support reading the magnetometer
//
// The chip has two possible I2C addresses, which can be selected by wiring the AD0 pin to either
// hot or ground:
//   - if AD0 is wired to ground, it uses the default I2C address of 0x71
//   - if AD0 is wired to hot, it uses the alternate I2C address of 0x69
//
// If you use the alternate address, your config file for this component must set its
// "use_alternate_i2c_address" boolean to true.
package mpu

import (
	"context"
	"time"

	"github.com/golang/geo/r3"
	"github.com/pkg/errors"
	"go.viam.com/rdk/components/board/genericlinux/buses"
	"go.viam.com/rdk/components/movementsensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
	"go.viam.com/rdk/utils"
	goutils "go.viam.com/utils"
)

var (
	// Model for viam supported tdk-invensense mpu9250 movement sensor.
	Model9250 = resource.NewModel("viam", "tdk-invensense", "mpu9250")

	// scales for various readings.
	accelScale float64
	gyroScale  float64
)

const (
	expectedConfigurationReadAddress = 0x71
	magnetometerAddress              = 0x0C
	magnetometerWhoAmI               = 0x00
	magnetometerWhoAmIReturn         = 0x48
)

func init() {
	resource.RegisterComponent(movementsensor.API, Model9250, resource.Registration[movementsensor.MovementSensor, *Config]{
		Constructor: newMpu9250,
	})
}

// newMpu9250 constructs a new Mpu9250 object.
func newMpu9250(
	ctx context.Context,
	deps resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (movementsensor.MovementSensor, error) {
	newConf, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		return nil, err
	}

	bus, err := buses.NewI2cBus(newConf.I2cBus)
	if err != nil {
		return nil, err
	}
	return makeMpu9250(ctx, deps, conf, logger, bus)
}

// This function is separated from NewMpu9250 solely so you can inject a mock I2C bus in tests.
func makeMpu9250(
	ctx context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
	bus buses.I2C,
) (movementsensor.MovementSensor, error) {
	newConf, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		return nil, err
	}

	var address byte
	if newConf.UseAlternateI2CAddress {
		address = alternateAddress
	} else {
		address = expectedDefaultAddress
	}
	logger.CDebugf(ctx, "Using address %d for MPU9250 sensor", address)

	sensor := &mpu{
		Named:      conf.ResourceName().AsNamed(),
		bus:        bus,
		i2cAddress: address,
		magAddress: magnetometerAddress,
		logger:     logger,
		// On overloaded boards, the I2C bus can become flaky. Only report errors if at least 5 of
		// the last 10 attempts to talk to the device have failed.
		err: movementsensor.NewLastError(10, 5),
	}

	// To check that we're able to talk to the chip, we should be able to read register 117 and get
	// back the device's expected configuration read address (0x71).
	defaultAddress, err := sensor.readByte(ctx, defaultAddressRegister)
	if err != nil {
		return nil, addressReadError(err, address, newConf.I2cBus)
	}
	if defaultAddress != expectedConfigurationReadAddress {
		return nil, unexpectedDeviceError(address, defaultAddress)
	}

	// The chip starts out in standby mode (the Sleep bit in the power management register defaults
	// to 1). Set it to measurement mode (by turning off the Sleep bit) so we can get data from it.
	// To do this, we set register 107 to 0.
	err = sensor.writeByte(ctx, 107, 0)
	if err != nil {
		return nil, errors.Errorf("Unable to wake up MPU9250: '%s'", err.Error())
	}

	// enable passthrough
	err = sensor.writeByte(ctx, 37, 0x22)
	if err != nil {
		return nil, errors.Errorf("Unable to enable passthrough: '%s'", err.Error())
	}
	logger.Error("enabled passthrough successfully")

	err = sensor.writeByte(ctx, 38, 0x01)
	if err != nil {
		return nil, errors.Errorf("Unable to enable passthrough: '%s'", err.Error())
	}
	logger.Error("enabled passthrough successfully 2")

	// // read pass through status
	// passthroughStatus, err := sensor.readByte(ctx, defaultAddressRegister)
	// if err != nil {
	// 	return nil, errors.Errorf("Unable to read passthrough status: '%s'", err.Error())
	// }
	// logger.Errorf("PASSTHROUGH STATUS = %v", passthroughStatus>>7)

	// read who am i magnetometer
	defaultMagAddress, err := sensor.readMagByte(ctx, magnetometerWhoAmI)
	if defaultMagAddress != magnetometerWhoAmIReturn {
		logger.Errorf("mag address wrong. expected %v, got %v", magnetometerWhoAmIReturn, defaultMagAddress)
	}

	// set measurement scales
	gyroScale, accelScale, err = sensor.getReadingScales(ctx)
	if err != nil {
		return nil, err
	}

	// Now, turn on the background goroutine that constantly reads from the chip and stores data in
	// the object we created.
	sensor.workers = goutils.NewBackgroundStoppableWorkers(func(cancelCtx context.Context) {
		// Reading data a thousand times per second is probably fast enough.
		timer := time.NewTicker(time.Millisecond)
		defer timer.Stop()

		for {
			select {
			case <-timer.C:
				rawData, err := sensor.readBlock(cancelCtx, 59, 14)
				// Record `err` no matter what: even if it's nil, that's useful information.
				sensor.err.Set(err)
				if err != nil {
					sensor.logger.CErrorf(ctx, "error reading MPU9250 sensor: '%s'", err)
					continue
				}

				linearAcceleration := toLinearAcceleration(rawData[0:6])
				temperature := float64(utils.Int16FromBytesBE(rawData[6:8]))/333.87 + 21.0
				angularVelocity := toAngularVelocity(rawData[8:14])

				// Lock the mutex before modifying the state within the object. By keeping the mutex
				// unlocked for everything else, we maximize the time when another thread can read the
				// values.
				sensor.mu.Lock()
				sensor.linearAcceleration = linearAcceleration
				sensor.temperature = temperature
				sensor.angularVelocity = angularVelocity
				sensor.mu.Unlock()
			case <-cancelCtx.Done():
				return
			}
		}
	})

	return sensor, nil
}

func (mpu *mpu) readMagByte(ctx context.Context, register byte) (byte, error) {
	result, err := mpu.readMagBlock(ctx, register, 1)
	if err != nil {
		return 0, err
	}
	return result[0], err
}

func (mpu *mpu) readMagBlock(ctx context.Context, register byte, length uint8) ([]byte, error) {
	handle, err := mpu.bus.OpenHandle(mpu.magAddress)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := handle.Close()
		if err != nil {
			mpu.logger.CError(ctx, err)
		}
	}()

	results, err := handle.ReadBlockData(ctx, register, length)
	return results, err
}

func (mpu *mpu) writeMagByte(ctx context.Context, register, value byte) error {
	handle, err := mpu.bus.OpenHandle(mpu.magAddress)
	if err != nil {
		return err
	}
	defer func() {
		err := handle.Close()
		if err != nil {
			mpu.logger.CError(ctx, err)
		}
	}()

	return handle.WriteByteData(ctx, register, value)
}

func (mpu *mpu) getReadingScales(ctx context.Context) (float64, float64, error) {
	var gyroScale, accelScale float64
	// get gyroscope scale
	result, err := mpu.readByte(ctx, 27)
	if err != nil {
		return 0, 0, err
	}
	switch result {
	case 0o0:
		gyroScale = 250.0 / 32768.0
	case 0o1:
		gyroScale = 500.0 / 32768.0
	case 10:
		gyroScale = 1000.0 / 32768.0
	case 11:
		gyroScale = 2000.0 / 32768.0
	default:
	}

	// get accelerometer scale
	result, err = mpu.readByte(ctx, 28)
	if err != nil {
		return 0, 0, err
	}
	switch result {
	case 0o0:
		accelScale = 2.0 / 32768.0
	case 0o1:
		accelScale = 4.0 / 32768.0
	case 10:
		accelScale = 8.0 / 32768.0
	case 11:
		accelScale = 16.0 / 32768.0
	default:
	}
	return gyroScale, accelScale, nil
}

// Given a value, scales it so that the range of int16s becomes the range of +/- maxValue.
func setScale9250(value int, maxValue float64) float64 {
	return float64(value) * maxValue
}

// A helper function to abstract out shared code: takes 6 bytes and gives back AngularVelocity, in
// radians per second.
func toAngularVelocity9250(data []byte) spatialmath.AngularVelocity {
	gx := int(utils.Int16FromBytesBE(data[0:2]))
	gy := int(utils.Int16FromBytesBE(data[2:4]))
	gz := int(utils.Int16FromBytesBE(data[4:6]))

	// gyroScale is the maximum degrees per second measurable
	return spatialmath.AngularVelocity{
		X: setScale9250(gx, gyroScale),
		Y: setScale9250(gy, gyroScale),
		Z: setScale9250(gz, gyroScale),
	}
}

// A helper function that takes 6 bytes and gives back linear acceleration.
func toLinearAcceleration9250(data []byte) r3.Vector {
	x := int(utils.Int16FromBytesBE(data[0:2]))
	y := int(utils.Int16FromBytesBE(data[2:4]))
	z := int(utils.Int16FromBytesBE(data[4:6]))

	// The scale is +/- X Gs based on the calculated accelScale, but our units should be m/sec/sec.
	return r3.Vector{
		X: setScale9250(x, accelScale) * 9.81,
		Y: setScale9250(y, accelScale) * 9.81,
		Z: setScale9250(z, accelScale) * 9.81,
	}
}
