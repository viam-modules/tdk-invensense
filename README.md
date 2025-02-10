# [`tdk-invensense` module](https://github.com/viam-modules/tdk-invensense)

This [tdk-invensense module](https://app.viam.com/module/viam/tdk-invensense) implements a tdk-invensense [mpu6050 movement_sensor](https://invensense.tdk.com/products/motion-tracking/6-axis/mpu-6050/), a combination gyroscope and accelerometer, using the [`rdk:component:movement_sensor` API](https://docs.viam.com/appendix/apis/components/movement_sensor/).

> [!NOTE]
> Before configuring your movement_sensor, you must [create a machine](https://docs.viam.com/cloud/machines/#add-a-new-machine).

Navigate to the [**CONFIGURE** tab](https://docs.viam.com/configure/) of your [machine](https://docs.viam.com/fleet/machines/) in the [Viam app](https://app.viam.com/).
[Add movement_sensor / tdk-invensense:mpu6050 to your machine](https://docs.viam.com/configure/#components).

## Configure your mpu6050 movement_sensor

On the new component panel, copy and paste the following attribute template into your movement_sensor's attributes field:

```json
{
    "i2c_bus": "<your-i2c-bus-index-on-board>",
    "use_alt_i2c_address": <boolean>
}
```

### Attributes

The following attributes are available for `viam:tdk-invensense:mpu6050` movement_sensors:

| Attribute | Type | Required? | Description |
| --------- | ---- | --------- | ----------  |
| `i2c_bus`             | string  | **Required** | The index of the I2C bus on the [board](https://docs.viam.com/components/board/) that your movement sensor is wired to. |
| `use_alt_i2c_address` | boolean | Optional     | Depends on whether you wire AD0 low (leaving the default address of 0x68) or high (making the address 0x69). If high, set `true`. If low, set `false`. Default: `false` |

### Example configuration

```json
  {
    "i2c_bus": "1"
  }
```

### Next Steps

- To test your movement_sensor, expand the **TEST** section of its configuration pane or go to the [**CONTROL** tab](https://docs.viam.com/fleet/control/).
- To write code against your movement_sensor, use one of the [available SDKs](https://docs.viam.com/sdks/).
- To view examples using a movement_sensor component, explore [these tutorials](https://docs.viam.com/tutorials/).
