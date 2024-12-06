//go:build linux

// Package mpu9250 implements the movementsensor interface for an MPU-9250 6-axis accelerometer. A
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
package mpu9250

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/golang/geo/r3"
	geo "github.com/kellydunn/golang-geo"
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
	Model = resource.NewModel("viam", "tdk-invensense", "mpu9250")

	// scales for various readings
	accelScale float64
	gyroScale  float64
)

const (
	defaultAddressRegister           = 117
	expectedDefaultAddress           = 0x68
	expectedConfigurationReadAddress = 0x71
	alternateAddress                 = 0x69
)

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
		Constructor: newMpu9250,
	})
}

type mpu9250 struct {
	resource.Named
	resource.AlwaysRebuild
	bus        buses.I2C
	i2cAddress byte
	mu         sync.Mutex

	// The 3 things we can measure: lock the mutex before reading or writing these.
	angularVelocity    spatialmath.AngularVelocity
	temperature        float64
	linearAcceleration r3.Vector
	// Stores the most recent error from the background goroutine
	err movementsensor.LastError

	workers *goutils.StoppableWorkers
	logger  logging.Logger
}

func addressReadError(err error, address byte, bus string) error {
	msg := fmt.Sprintf("can't read from I2C address %d on bus %s", address, bus)
	return errors.Wrap(err, msg)
}

func unexpectedDeviceError(address, defaultAddress byte) error {
	return errors.Errorf("unexpected non-MPU9250 device at address %d: response '%d'",
		address, defaultAddress)
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

	sensor := &mpu9250{
		Named:      conf.ResourceName().AsNamed(),
		bus:        bus,
		i2cAddress: address,
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

func (mpu *mpu9250) getReadingScales(ctx context.Context) (float64, float64, error) {
	var gyroScale, accelScale float64
	// get gyroscope scale
	result, err := mpu.readByte(ctx, 27)
	if err != nil {
		return 0, 0, err
	}
	switch result {
	case 00:
		gyroScale = 250.0 / 32768.0
	case 01:
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
	case 00:
		accelScale = 2.0 / 32768.0
	case 01:
		accelScale = 4.0 / 32768.0
	case 10:
		accelScale = 8.0 / 32768.0
	case 11:
		accelScale = 16.0 / 32768.0
	default:
	}
	return gyroScale, accelScale, nil
}

func (mpu *mpu9250) getAccelScale(ctx context.Context) (float64, error) {
	result, err := mpu.readByte(ctx, 28)
	if err != nil {
		return 0, err
	}
	switch result {
	case 00:
		return 2.0 / 32768.0, nil
	case 01:
		return 4.0 / 32768.0, nil
	case 10:
		return 8.0 / 32768.0, nil
	case 11:
		return 16.0 / 32768.0, nil
	default:
	}
	return 0, nil
}

func (mpu *mpu9250) readByte(ctx context.Context, register byte) (byte, error) {
	result, err := mpu.readBlock(ctx, register, 1)
	if err != nil {
		return 0, err
	}
	return result[0], err
}

func (mpu *mpu9250) readBlock(ctx context.Context, register byte, length uint8) ([]byte, error) {
	handle, err := mpu.bus.OpenHandle(mpu.i2cAddress)
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

func (mpu *mpu9250) writeByte(ctx context.Context, register, value byte) error {
	handle, err := mpu.bus.OpenHandle(mpu.i2cAddress)
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

// Given a value, scales it so that the range of int16s becomes the range of +/- maxValue.
func setScale(value int, maxValue float64) float64 {
	return float64(value) * maxValue
}

// A helper function to abstract out shared code: takes 6 bytes and gives back AngularVelocity, in
// radians per second.
func toAngularVelocity(data []byte) spatialmath.AngularVelocity {
	gx := int(utils.Int16FromBytesBE(data[0:2]))
	gy := int(utils.Int16FromBytesBE(data[2:4]))
	gz := int(utils.Int16FromBytesBE(data[4:6]))

	// maxRotation := 250.0 // Maximum degrees per second measurable in the default configuration
	return spatialmath.AngularVelocity{
		X: setScale(gx, gyroScale),
		Y: setScale(gy, gyroScale),
		Z: setScale(gz, gyroScale),
	}
}

// A helper function that takes 6 bytes and gives back linear acceleration.
func toLinearAcceleration(data []byte) r3.Vector {
	x := int(utils.Int16FromBytesBE(data[0:2]))
	y := int(utils.Int16FromBytesBE(data[2:4]))
	z := int(utils.Int16FromBytesBE(data[4:6]))

	// // The scale is +/- 2G's, but our units should be m/sec/sec.
	// maxAcceleration := 9.81 /* m/sec/sec */
	return r3.Vector{
		X: setScale(x, accelScale) * 9.81,
		Y: setScale(y, accelScale) * 9.81,
		Z: setScale(z, accelScale) * 9.81,
	}
}

func (mpu *mpu9250) AngularVelocity(ctx context.Context, extra map[string]interface{}) (spatialmath.AngularVelocity, error) {
	mpu.mu.Lock()
	defer mpu.mu.Unlock()
	return mpu.angularVelocity, mpu.err.Get()
}

func (mpu *mpu9250) LinearVelocity(ctx context.Context, extra map[string]interface{}) (r3.Vector, error) {
	return r3.Vector{}, movementsensor.ErrMethodUnimplementedLinearVelocity
}

func (mpu *mpu9250) LinearAcceleration(ctx context.Context, exta map[string]interface{}) (r3.Vector, error) {
	mpu.mu.Lock()
	defer mpu.mu.Unlock()

	lastError := mpu.err.Get()
	if lastError != nil {
		return r3.Vector{}, lastError
	}
	return mpu.linearAcceleration, nil
}

func (mpu *mpu9250) Orientation(ctx context.Context, extra map[string]interface{}) (spatialmath.Orientation, error) {
	return spatialmath.NewOrientationVector(), movementsensor.ErrMethodUnimplementedOrientation
}

func (mpu *mpu9250) CompassHeading(ctx context.Context, extra map[string]interface{}) (float64, error) {
	return 0, movementsensor.ErrMethodUnimplementedCompassHeading
}

func (mpu *mpu9250) Position(ctx context.Context, extra map[string]interface{}) (*geo.Point, float64, error) {
	return geo.NewPoint(0, 0), 0, movementsensor.ErrMethodUnimplementedPosition
}

func (mpu *mpu9250) Accuracy(ctx context.Context, extra map[string]interface{}) (*movementsensor.Accuracy, error) {
	return movementsensor.UnimplementedOptionalAccuracies(), nil
}

func (mpu *mpu9250) Readings(ctx context.Context, extra map[string]interface{}) (map[string]interface{}, error) {
	mpu.mu.Lock()
	defer mpu.mu.Unlock()

	readings := make(map[string]interface{})
	readings["linear_acceleration"] = mpu.linearAcceleration
	readings["temperature_celsius"] = mpu.temperature
	readings["angular_velocity"] = mpu.angularVelocity

	return readings, mpu.err.Get()
}

func (mpu *mpu9250) Properties(ctx context.Context, extra map[string]interface{}) (*movementsensor.Properties, error) {
	return &movementsensor.Properties{
		AngularVelocitySupported:    true,
		LinearAccelerationSupported: true,
	}, nil
}

func (mpu *mpu9250) Close(ctx context.Context) error {
	mpu.workers.Stop()

	mpu.mu.Lock()
	defer mpu.mu.Unlock()
	// Set the Sleep bit (bit 6) in the power control register (register 107).
	err := mpu.writeByte(ctx, 107, 1<<6)
	if err != nil {
		mpu.logger.CError(ctx, err)
	}
	return err
}
