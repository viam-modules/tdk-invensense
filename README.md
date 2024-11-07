# [`tdk-invensense` module](https://github.com/viam-modules/tdk-invensense)

This [tdk-invensense module](https://app.viam.com/module/viam/tdk-invensense) implements a tdk-invensense [mpu6050 movement_sensor](<LINK TO HARDWARE>), used for <DESCRIPTION> using the [`rdk:component:movement_sensor` API](https://docs.viam.com/appendix/apis/components/movement_sensor/).

> [!NOTE]
> Before configuring your movement_sensor, you must [create a machine](https://docs.viam.com/cloud/machines/#add-a-new-machine).

## Configure your mpu6050 movement_sensor

Navigate to the [**CONFIGURE** tab](https://docs.viam.com/configure/) of your [machine](https://docs.viam.com/fleet/machines/) in the [Viam app](https://app.viam.com/).
[Add movement_sensor / tdk-invensense:mpu6050 to your machine](https://docs.viam.com/configure/#components).

On the new component panel, copy and paste the following attribute template into your movement_sensor's attributes field:

```json
{
  <ATTRIBUTES>
}
```

### Attributes

The following attributes are available for `viam:tdk-invensense:mpu6050` movement_sensors:

<EXAMPLE !!>
| Attribute | Type | Required? | Description |
| --------- | ---- | --------- | ----------  |
| `i2c_bus` | string | **Required** | The index of the I<sup>2</sup>C bus on the board that the movement_sensor is wired to. |
| `i2c_address` | string | Optional | Default: `0x77`. The [I<sup>2</sup>C device address](https://learn.adafruit.com/i2c-addresses/overview) of the movement_sensor. |

## Example configuration

### `viam:tdk-invensense:mpu6050`
```json
  {
      "name": "<your-tdk-invensense-mpu6050-movement_sensor-name>",
      "model": "viam:tdk-invensense:mpu6050",
      "type": "movement_sensor",
      "namespace": "rdk",
      "attributes": {
      },
      "depends_on": []
  }
```

### Next Steps
- To test your movement_sensor, expand the **TEST** section of its configuration pane or go to the [**CONTROL** tab](https://docs.viam.com/fleet/control/).
- To write code against your movement_sensor, use one of the [available SDKs](https://docs.viam.com/sdks/).
- To view examples using a movement_sensor component, explore [these tutorials](https://docs.viam.com/tutorials/).