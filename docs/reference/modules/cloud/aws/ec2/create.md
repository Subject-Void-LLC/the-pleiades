---
status: beta
---

# cloud.aws.ec2.create

Launches an EC2 instance via the AWS API.

Makes sure an instance tagged Name=name exists among the account/region's non-terminated instances, launching one from image_id if none does. This is a narrow slice of amazon.aws.ec2_instance: idempotency here is existence of the Name tag only, not a comparison of a matching instance's configuration against what was requested. An instance already present under that name is left exactly as it is, regardless of whether its image or instance type match; this method never recreates. The target device is the AWS account/region context itself (an aws_account inventory item), not a device this task reaches over any transport.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `AWSAPICapable` |
| Transports | - |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The Name tag to find or create an instance under. |
| `image_id` | `string` | yes | - | The AMI id to launch from. Ignored when an instance already exists under name. |
| `instance_type` | `string` | yes | - | The EC2 instance type (e.g. t3.micro). Ignored when an instance already exists under name. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `instance_id` | `string` | when an instance exists | The instance this task found or launched. |
| `diff` | `dict` | always | What the account reported about the Name-tagged instance before this task and after it (exists, instance_id, state). Recorded even on a run that changed nothing. |
| `inverse` | `dict` | when this task launched a new instance | The cloud.aws.ec2.terminate task that undoes this run. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that launched a fresh instance (name did not already exist among non-terminated instances) emits a cloud.aws.ec2.terminate naming the launched instance_id. A run that found an existing match emits nothing, the same as every other converged run in this catalog, even though this method does not compare that existing instance's image or type against what was requested.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `cloud.aws.ec2.terminate`

## Examples

Launch a small instance:

```yaml
- name: Launch the build agent
  cloud.aws.ec2.create:
    name: build-agent-1
    image_id: ami-0abcdef1234567890
    instance_type: t3.micro
```

