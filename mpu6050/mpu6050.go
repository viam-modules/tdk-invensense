//go:build linux

// Package mpu6050 implements the movementsensor interface for an MPU-6050 6-axis accelerometer. A
// datasheet for this chip is at
// https://components101.com/sites/default/files/component_datasheet/MPU6050-DataSheet.pdf and a
// description of the I2C registers is at
// https://download.datasheets.com/pdfs/2015/3/19/8/3/59/59/invse_/manual/5rm-mpu-6000a-00v4.2.pdf
//
// We support reading the accelerometer, gyroscope, and thermometer data off of the chip. We do not
// yet support using the digital interrupt pin to notify on events (freefall, collision, etc.),
// nor do we yet support using the secondary I2C connection to add an external clock or
// magnetometer.
//
// The chip has two possible I2C addresses, which can be selected by wiring the AD0 pin to either
// hot or ground:
//   - if AD0 is wired to ground, it uses the default I2C address of 0x68
//   - if AD0 is wired to hot, it uses the alternate I2C address of 0x69
//
// If you use the alternate address, your config file for this component must set its
// "use_alt_i2c_address" boolean to true.
package mpu6050

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
	"go.viam.com/rdk/data"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
	"go.viam.com/rdk/utils"
	goutils "go.viam.com/utils"
)

const (
	defaultAddressRegister = 117
	expectedDefaultAddress = 0x68
	alternateAddress       = 0x69

	powerRegister = 107
	dataRegister  = 59 // ACCEL_XOUT_H: start of the 14-byte accel/temperature/gyro block

	// Reading data a thousand times per second is probably fast enough.
	pollInterval = time.Millisecond
	// If every read fails for this long, assume the chip was reset or disconnected and go back to
	// probing and waking it up.
	reinitAfter = time.Second
	// While the chip is unreachable, retry with exponential backoff between these bounds.
	minRetryInterval = 100 * time.Millisecond
	maxRetryInterval = 30 * time.Second
	// Status reports unhealthy after this many consecutive failed reads, matching the LastError
	// threshold used for API calls.
	unhealthyAfterFailures = 5
)

// errNotReady is reported before the chip has ever been initialized successfully.
var errNotReady = errors.New("MPU6050 has not been initialized yet")

type mpu6050 struct {
	resource.Named
	bus        buses.I2C
	busName    string
	i2cAddress byte
	mu         sync.Mutex

	// The 3 things we can measure: lock the mutex before reading or writing these.
	angularVelocity    spatialmath.AngularVelocity
	temperature        float64
	linearAcceleration r3.Vector
	// When the background goroutine last read the chip successfully.
	lastReadTime time.Time
	// ready is true once the chip has been identified and woken up. While it's false, the
	// background goroutine keeps retrying, and initErr holds the latest reason it failed.
	ready   bool
	initErr error
	// Consecutive failed reads and the most recent read error, for Status. Status can't use err
	// because LastError.Get clears its history, which would hide errors from API callers.
	readFailures int
	lastReadErr  error
	// Stores the most recent error from the background goroutine
	err movementsensor.LastError

	workers *goutils.StoppableWorkers
	logger  logging.Logger
}

func addressReadError(err error, address byte, bus string) error {
	msg := fmt.Sprintf("can't read from I2C address %d on bus %s", address, bus)
	return errors.Wrap(err, msg)
}

var errUnexpectedDevice = errors.New("unexpected non-MPU6050 device")

func unexpectedDeviceError(address, defaultAddress byte) error {
	return errors.Wrapf(errUnexpectedDevice, "address %d responded with '%d'", address, defaultAddress)
}

// NewMpu6050 constructs a new Mpu6050 object.
func NewMpu6050(
	ctx context.Context,
	logger logging.Logger,
	name string,
	busName string,
	useAlternateI2CAddress bool,
) (movementsensor.MovementSensor, error) {
	bus, err := buses.NewI2cBus(busName)
	if err != nil {
		return nil, err
	}
	return makeMpu6050(ctx, logger, movementsensor.Named(name), busName, bus, useAlternateI2CAddress)
}

// newMpu6050 constructs a new Mpu6050 object.
func newMpu6050(
	ctx context.Context,
	_ resource.Dependencies,
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
	return makeMpu6050(ctx, logger, conf.ResourceName(), newConf.I2cBus, bus, newConf.UseAlternateI2CAddress)
}

// This function is separated from NewMpu6050 solely so you can inject a mock I2C bus in tests.
func makeMpu6050(
	ctx context.Context,
	logger logging.Logger,
	name resource.Name,
	busName string,
	bus buses.I2C,
	useAlternateI2CAddress bool,
) (movementsensor.MovementSensor, error) {
	var address byte
	if useAlternateI2CAddress {
		address = alternateAddress
	} else {
		address = expectedDefaultAddress
	}
	logger.CDebugf(ctx, "Using address %d for MPU6050 sensor", address)

	sensor := &mpu6050{
		Named:      name.AsNamed(),
		bus:        bus,
		busName:    busName,
		i2cAddress: address,
		logger:     logger,
		// On overloaded boards, the I2C bus can become flaky. Only report errors if at least 5 of
		// the last 10 attempts to talk to the device have failed.
		err: movementsensor.NewLastError(10, 5),
	}

	// Try to bring the chip up now so configuration mistakes show up as build errors. A different
	// device at the address is a configuration error, so fail. If nothing answers (loose wire, hung
	// chip, flaky bus), build the resource anyway and keep retrying in the background. Failing here
	// would make viam-server rebuild the resource every few seconds and log a build error each time.
	if err := sensor.initChip(ctx); err != nil {
		if errors.Is(err, errUnexpectedDevice) {
			return nil, err
		}
		sensor.setNotReady(err)
		logger.CWarnf(ctx, "MPU6050 not responding at startup; retrying in the background: %v", err)
	}

	sensor.workers = goutils.NewBackgroundStoppableWorkers(sensor.pollLoop)
	return sensor, nil
}

// initChip checks that an MPU6050 is at the configured address and wakes it up.
func (mpu *mpu6050) initChip(ctx context.Context) error {
	// To check that we're able to talk to the chip, we should be able to read register 117 and get
	// back the device's non-alternative address (0x68).
	defaultAddress, err := mpu.readByte(ctx, defaultAddressRegister)
	if err != nil {
		return addressReadError(err, mpu.i2cAddress, mpu.busName)
	}
	if defaultAddress != expectedDefaultAddress {
		return unexpectedDeviceError(mpu.i2cAddress, defaultAddress)
	}

	// The chip starts out in standby mode (the Sleep bit in the power management register defaults
	// to 1), and goes back to it after a power glitch. Set it to measurement mode by writing 0 to
	// register 107 so we can get data from it.
	if err := mpu.writeByte(ctx, powerRegister, 0); err != nil {
		return errors.Wrap(err, "unable to wake up MPU6050")
	}

	mpu.mu.Lock()
	mpu.ready = true
	mpu.initErr = nil
	mpu.mu.Unlock()
	return nil
}

func (mpu *mpu6050) setNotReady(err error) {
	mpu.mu.Lock()
	defer mpu.mu.Unlock()
	mpu.ready = false
	mpu.initErr = err
}

func (mpu *mpu6050) isReady() bool {
	mpu.mu.Lock()
	defer mpu.mu.Unlock()
	return mpu.ready
}

// pollLoop runs in the background. It reads the chip while it's healthy and, when it isn't,
// re-probes and re-wakes it with exponential backoff. It only logs when the state changes.
func (mpu *mpu6050) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	backoff := minRetryInterval
	var failingSince time.Time
	for {
		if !mpu.isReady() {
			if err := mpu.initChip(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				mpu.setNotReady(err)
				if !goutils.SelectContextOrWait(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, maxRetryInterval)
				continue
			}
			mpu.logger.CInfof(ctx, "MPU6050 initialized on bus %s at address 0x%02x", mpu.busName, mpu.i2cAddress)
			backoff = minRetryInterval
			failingSince = time.Time{}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		rawData, err := mpu.readBlock(ctx, dataRegister, 14)
		// Record `err` no matter what: even if it's nil, that's useful information.
		mpu.err.Set(err)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			mpu.mu.Lock()
			mpu.readFailures++
			mpu.lastReadErr = err
			mpu.mu.Unlock()
			switch {
			case failingSince.IsZero():
				failingSince = time.Now()
				mpu.logger.CErrorf(ctx, "error reading MPU6050 sensor: '%s'", err)
			case time.Since(failingSince) > reinitAfter:
				mpu.logger.CWarnf(ctx, "MPU6050 reads failing for over %s; re-initializing with backoff", reinitAfter)
				mpu.setNotReady(err)
			}
			continue
		}
		if !failingSince.IsZero() {
			mpu.logger.CInfo(ctx, "MPU6050 sensor reads recovered")
			failingSince = time.Time{}
		}

		linearAcceleration := toLinearAcceleration(rawData[0:6])
		// Taken straight from the MPU6050 register map. Yes, these are weird constants.
		temperature := float64(utils.Int16FromBytesBE(rawData[6:8]))/340.0 + 36.53
		angularVelocity := toAngularVelocity(rawData[8:14])

		// Lock the mutex before modifying the state within the object. By keeping the mutex
		// unlocked for everything else, we maximize the time when another thread can read the
		// values.
		mpu.mu.Lock()
		mpu.linearAcceleration = linearAcceleration
		mpu.temperature = temperature
		mpu.angularVelocity = angularVelocity
		mpu.lastReadTime = time.Now()
		mpu.readFailures = 0
		mpu.lastReadErr = nil
		mpu.mu.Unlock()
	}
}

// currentError returns the error API calls should report. The caller must hold mpu.mu. Calls from
// the data manager get data.ErrNoCaptureToStore instead, so a missing sensor skips captures quietly
// rather than logging a failed capture every interval.
func (mpu *mpu6050) currentError(extra map[string]interface{}) error {
	var err error
	if !mpu.ready {
		err = mpu.initErr
		if err == nil {
			err = errNotReady
		}
	} else {
		err = mpu.err.Get()
	}
	if err != nil && fromDataManager(extra) {
		return data.ErrNoCaptureToStore
	}
	return err
}

func fromDataManager(extra map[string]interface{}) bool {
	v, ok := extra[data.FromDMString].(bool)
	return ok && v
}

func (mpu *mpu6050) readByte(ctx context.Context, register byte) (byte, error) {
	result, err := mpu.readBlock(ctx, register, 1)
	if err != nil {
		return 0, err
	}
	return result[0], err
}

func (mpu *mpu6050) readBlock(ctx context.Context, register byte, length uint8) ([]byte, error) {
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

func (mpu *mpu6050) writeByte(ctx context.Context, register, value byte) error {
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
	return float64(value) * maxValue / (1 << 15)
}

// A helper function to abstract out shared code: takes 6 bytes and gives back AngularVelocity, in
// degrees per second.
func toAngularVelocity(data []byte) spatialmath.AngularVelocity {
	gx := int(utils.Int16FromBytesBE(data[0:2]))
	gy := int(utils.Int16FromBytesBE(data[2:4]))
	gz := int(utils.Int16FromBytesBE(data[4:6]))

	maxRotation := 250.0 // Maximum degrees per second measurable in the default configuration
	return spatialmath.AngularVelocity{
		X: setScale(gx, maxRotation),
		Y: setScale(gy, maxRotation),
		Z: setScale(gz, maxRotation),
	}
}

// A helper function that takes 6 bytes and gives back linear acceleration.
func toLinearAcceleration(data []byte) r3.Vector {
	x := int(utils.Int16FromBytesBE(data[0:2]))
	y := int(utils.Int16FromBytesBE(data[2:4]))
	z := int(utils.Int16FromBytesBE(data[4:6]))

	// The scale is +/- 2G's, but our units should be m/sec/sec.
	maxAcceleration := 2.0 * 9.81 /* m/sec/sec */
	return r3.Vector{
		X: setScale(x, maxAcceleration),
		Y: setScale(y, maxAcceleration),
		Z: setScale(z, maxAcceleration),
	}
}

func (mpu *mpu6050) AngularVelocity(ctx context.Context, extra map[string]interface{}) (spatialmath.AngularVelocity, error) {
	mpu.mu.Lock()
	defer mpu.mu.Unlock()
	if err := mpu.currentError(extra); err != nil {
		return spatialmath.AngularVelocity{}, err
	}
	return mpu.angularVelocity, nil
}

func (mpu *mpu6050) LinearVelocity(ctx context.Context, extra map[string]interface{}) (r3.Vector, error) {
	return r3.Vector{}, movementsensor.ErrMethodUnimplementedLinearVelocity
}

func (mpu *mpu6050) LinearAcceleration(ctx context.Context, extra map[string]interface{}) (r3.Vector, error) {
	mpu.mu.Lock()
	defer mpu.mu.Unlock()
	if err := mpu.currentError(extra); err != nil {
		return r3.Vector{}, err
	}
	return mpu.linearAcceleration, nil
}

func (mpu *mpu6050) Orientation(ctx context.Context, extra map[string]interface{}) (spatialmath.Orientation, error) {
	return spatialmath.NewOrientationVector(), movementsensor.ErrMethodUnimplementedOrientation
}

func (mpu *mpu6050) CompassHeading(ctx context.Context, extra map[string]interface{}) (float64, error) {
	return 0, movementsensor.ErrMethodUnimplementedCompassHeading
}

func (mpu *mpu6050) Position(ctx context.Context, extra map[string]interface{}) (*geo.Point, float64, error) {
	return geo.NewPoint(0, 0), 0, movementsensor.ErrMethodUnimplementedPosition
}

func (mpu *mpu6050) Accuracy(ctx context.Context, extra map[string]interface{}) (*movementsensor.Accuracy, error) {
	return movementsensor.UnimplementedOptionalAccuracies(), nil
}

func (mpu *mpu6050) Readings(ctx context.Context, extra map[string]interface{}) (map[string]interface{}, error) {
	mpu.mu.Lock()
	defer mpu.mu.Unlock()
	if err := mpu.currentError(extra); err != nil {
		return nil, err
	}

	readings := make(map[string]interface{})
	readings["linear_acceleration"] = mpu.linearAcceleration
	readings["temperature_celsius"] = mpu.temperature
	readings["angular_velocity"] = mpu.angularVelocity
	return readings, nil
}

func (mpu *mpu6050) Properties(ctx context.Context, extra map[string]interface{}) (*movementsensor.Properties, error) {
	return &movementsensor.Properties{
		AngularVelocitySupported:    true,
		LinearAccelerationSupported: true,
	}, nil
}

// Status reports the sensor's bus configuration and the health of the background read loop.
func (mpu *mpu6050) Status(ctx context.Context) (map[string]interface{}, error) {
	mpu.mu.Lock()
	lastRead := mpu.lastReadTime
	ready := mpu.ready
	var err error
	switch {
	case !ready:
		err = mpu.initErr
		if err == nil {
			err = errNotReady
		}
	case mpu.readFailures >= unhealthyAfterFailures:
		err = mpu.lastReadErr
	}
	mpu.mu.Unlock()

	status := map[string]interface{}{
		"i2c_bus":     mpu.busName,
		"i2c_address": fmt.Sprintf("0x%02x", mpu.i2cAddress),
		"initialized": ready,
		"healthy":     err == nil,
		"last_error":  "",
	}
	if err != nil {
		status["last_error"] = err.Error()
	}
	if lastRead.IsZero() {
		status["last_successful_read"] = ""
	} else {
		status["last_successful_read"] = lastRead.UTC().Format(time.RFC3339Nano)
	}
	return status, nil
}

// Close stops the background reader and puts the chip back to sleep. A chip that isn't responding
// can't be put to sleep, and that isn't worth failing Close over, so it only logs a warning.
func (mpu *mpu6050) Close(ctx context.Context) error {
	mpu.workers.Stop()

	mpu.mu.Lock()
	defer mpu.mu.Unlock()
	if !mpu.ready {
		return nil
	}
	mpu.ready = false
	// Set the Sleep bit (bit 6) in the power control register (register 107).
	if err := mpu.writeByte(ctx, powerRegister, 1<<6); err != nil {
		mpu.logger.CWarnf(ctx, "unable to put MPU6050 to sleep: %v", err)
	}
	return nil
}
