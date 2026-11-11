# [`tdk-invensense` module](https://app.viam.com/module/viam/tdk-invensense)

This module implements the [`rdk:component:movement_sensor` API](https://docs.viam.com/reference/apis/components/movement-sensor/) for the TDK InvenSense [MPU-6050](https://invensense.tdk.com/products/motion-tracking/6-axis/mpu-6050/), a 6-axis gyroscope and accelerometer that also has a temperature sensor. The sensor connects over I2C.

It provides one model:

* `viam:tdk-invensense:mpu6050`: an MPU-6050 connected to an I2C bus on a Linux board.

## Requirements

* A Linux machine running `viam-server`, on `linux/amd64` or `linux/arm64`. Raspberry Pi and other single-board computers work. Follow [Set up your first machine](https://docs.viam.com/set-up-a-machine/first-machine/) if you haven't yet.
* I2C enabled on the board. On a Raspberry Pi, you can enable it with `sudo raspi-config` (**Interface Options → I2C**), or set `"enable_i2c": true` in the [`raspberry-pi` board](https://app.viam.com/module/viam/raspberry-pi) config.
* The sensor wired to the board's I2C pins (SDA, SCL, VCC, GND). Use the AD0 pin to pick the I2C address:
  * AD0 to ground (or left unconnected on most breakout boards): address `0x68`, the default.
  * AD0 to VCC: address `0x69`. Set `use_alt_i2c_address` to `true`.

To check the wiring, run `i2cdetect -y <bus>` on the board. You should see `68` or `69`.

## Configure your mpu6050 movement_sensor

Go to the **CONFIGURE** tab of your machine in the [Viam app](https://app.viam.com) and [add a movement sensor](https://docs.viam.com/hardware/common-components/add-a-movement-sensor/). Search for `tdk-invensense:mpu6050`, then paste this attribute template into the component's attributes field:

```json
{
  "i2c_bus": "<your-i2c-bus-index-on-board>",
  "use_alt_i2c_address": <boolean>
}
```

### Attributes

| Attribute             | Type    | Required?    | Description |
| --------------------- | ------- | ------------ | ----------- |
| `i2c_bus`             | string  | **Required** | The index of the I2C bus the sensor is wired to. For example, `"1"` means `/dev/i2c-1`, the default bus on a Raspberry Pi's GPIO header. |
| `use_alt_i2c_address` | boolean | Optional     | Set to `true` if AD0 is wired high (address `0x69`). Default: `false` (address `0x68`). |

### Example configuration

```json
{
  "i2c_bus": "1"
}
```

## Supported API methods

The sensor runs in its power-on default ranges: ±2 g for the accelerometer and ±250 °/s for the gyroscope. A background loop reads the chip about 1,000 times per second, and API calls return the most recent values.

| Method               | Supported | Returns |
| -------------------- | --------- | ------- |
| `LinearAcceleration` | Yes       | Linear acceleration in m/s². |
| `AngularVelocity`    | Yes       | Angular velocity in degrees per second. |
| `Readings`           | Yes       | `linear_acceleration`, `angular_velocity`, and `temperature_celsius`. |
| `Properties`         | Yes       | Reports linear acceleration and angular velocity as supported. |
| `Accuracy`           | Yes       | Reports no accuracy data. |
| `Status`             | Yes       | See [Status](#status). |
| `Position`, `LinearVelocity`, `Orientation`, `CompassHeading` | No | Unimplemented error. |

The chip has no magnetometer, so it can't report heading or absolute orientation.

### Errors and recovery

The I2C bus can be flaky on busy boards, so a single failed read doesn't cause an error. Methods return an error only when at least 5 of the last 10 reads failed.

If the chip stops answering (a loose connector, a hung chip, or a power glitch), the component keeps running instead of failing:

* **At startup:** if nothing answers at the configured address, the component is still created, and API calls return an error until the chip responds. A *different* device at that address is a configuration error and still fails the build.
* **While running:** if every read fails for more than a second, the module goes back to probing and waking the chip. It retries with exponential backoff from 100 ms up to 30 s. When the chip answers again, readings resume without a restart, and the chip is woken up again in case it was reset.
* **Data capture:** while the chip isn't answering, the data manager skips captures quietly instead of logging a failed capture every interval.
* **Logs:** the module logs once when reads start failing, once when it starts re-initializing, and once when the chip comes back.

### Status

`Status` returns:

| Key                    | Example                            | Description |
| ---------------------- | ---------------------------------- | ----------- |
| `i2c_bus`              | `"1"`                              | The configured I2C bus. |
| `i2c_address`          | `"0x68"`                           | The I2C address in use. |
| `initialized`          | `true`                             | `false` while the module is still trying to find and wake the chip. |
| `healthy`              | `true`                             | `false` when the chip isn't initialized or the last 5 reads failed (see [Errors and recovery](#errors-and-recovery)). |
| `last_error`           | `""`                               | Why the sensor is unhealthy, or an empty string when it's healthy. |
| `last_successful_read` | `"2026-11-11T14:30:00.123456789Z"` | UTC timestamp of the most recent successful read, or an empty string if the sensor hasn't been read yet. |

## Development

You need Go (the version in `go.mod`) and `make`. Lint and test tools are pinned in `go.mod` as `tool` dependencies, so you don't install anything else.

| Command       | What it does |
| ------------- | ------------ |
| `make build`  | Builds a static `bin/tdk-invensense` binary. Set `GOARCH=arm64` to cross-compile. |
| `make module` | Builds and packages `bin/module.tar.gz` for the registry. |
| `make test`   | Runs unit tests with the race detector, using a mocked I2C bus. |
| `make lint`   | Runs `go mod tidy`, `go vet`, and `golangci-lint --fix`. |
| `make update` | Updates `go.viam.com/rdk` and `go.viam.com/api` to their latest versions. |

To try the driver on a board without `viam-server`, run `go run ./cmdlocal`. It reads I2C bus `1` once a second for 30 seconds and logs the values.

### Releasing

CI (`.github/workflows/ci.yml`) builds, tests, and lints every pull request and every push to `main`.

To publish a new version, create a GitHub release with a semver tag such as `0.0.2`. `.github/workflows/deploy.yml` then uses [`viamrobotics/build-action`](https://github.com/viamrobotics/build-action) to build every architecture in `meta.json` in Viam's cloud build and upload it to the registry. You can also run the Deploy workflow manually and enter a version. The workflow needs the `VIAM_DEV_API_KEY_ID` and `VIAM_DEV_API_KEY` secrets.
