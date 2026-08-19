---
status: beta
---

# cloud.aws.s3.delete_bucket

Deletes an S3 bucket via the AWS API.

Deletes bucket if it exists; a no-op otherwise. This method does not empty a non-empty bucket first: AWS itself refuses to delete one that still holds objects, and that refusal is the safety rail, not an error this method routes around.

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
| `bucket` | `string` | yes | - | The bucket name to delete. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `bucket` | `string` | always | The bucket this task acted on. |
| `diff` | `dict` | always | Whether the bucket existed before this task and after it. Recorded even on a run that changed nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Bucket names are globally unique across all of AWS, so a bucket this task just deleted may be claimed by an unrelated account before any inverse would run; there is no safe cloud.aws.s3.create_bucket to record.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `cloud.aws.s3.create_bucket`

## Examples

Delete a bucket:

```yaml
- name: Remove the release artifacts bucket
  fqcn: cloud.aws.s3.delete_bucket
  params:
    bucket: my-release-artifacts
```

