---
status: beta
---

# cloud.aws.ec2.terminate

Terminates an EC2 instance via the AWS API.

Terminates the instance named by instance_id. A no-op if AWS has no record of that id at all, or if it is already terminated. Unlike cloud.aws.ec2.create, this takes an exact instance_id rather than a Name-tag lookup: terminating by a fuzzy match is a worse default than requiring the exact resource for a destructive action.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `AWSAPICapable` |
| Transports | - |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `instance_id` | `string` | yes | - | The instance id to terminate. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `instance_id` | `string` | always | The instance this task acted on. |
| `diff` | `dict` | always | What the account reported about the instance before this task and after it (exists, instance_id, state). Recorded even on a run that changed nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A terminated instance's storage (unless an EBS volume was explicitly detached beforehand, which this method does not do) and identity are gone; there is nothing a cloud.aws.ec2.create could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `cloud.aws.ec2.create`

## Examples

Terminate an instance:

```yaml
- name: Tear down the build agent
  cloud.aws.ec2.terminate:
    instance_id: i-0123456789abcdef0
```

