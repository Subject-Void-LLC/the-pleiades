---
status: beta
---

# cloud.aws.s3.create_bucket

Creates an S3 bucket via the AWS API.

Makes sure bucket exists in the account/region, creating it if it does not. Idempotent on existence alone: this method has no bucket configuration surface (versioning, encryption, policy) to compare or converge, the same restraint every other narrowly-scoped method in this catalog applies against its own upstream's larger surface. The target device is the AWS account/region context itself (an aws_account inventory item), not a device this task reaches over any transport.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `AWSAPICapable` |
| Transports | - |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `bucket` | `string` | yes | - | The bucket name to create or leave alone. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `bucket` | `string` | always | The bucket this task acted on. |
| `diff` | `dict` | always | Whether the bucket existed before this task and after it. Recorded even on a run that changed nothing. |
| `inverse` | `dict` | when this task created the bucket | The cloud.aws.s3.delete_bucket task that undoes this run. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the bucket (it did not already exist) emits a cloud.aws.s3.delete_bucket naming it. A run that found the bucket already present emits nothing, the same as every other converged run in this catalog.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `cloud.aws.s3.delete_bucket`

## Examples

Create a bucket:

```yaml
- name: Create the release artifacts bucket
  cloud.aws.s3.create_bucket:
    bucket: my-release-artifacts
```

