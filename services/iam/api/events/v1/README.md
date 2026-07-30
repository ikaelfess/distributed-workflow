# Email delivery request v1

IAM publishes `iam.email-delivery-request` version 1 records to the `iam.email-delivery-request.v1` Kafka topic. The Kafka key and `event_id` are the same stable UUIDv7. Consumers must deduplicate by that ID because relay recovery can publish a record more than once.

The Kafka value follows `email-delivery-request.schema.json`. Its envelope contains an `email-payload.schema.json` document encrypted with AES-256-GCM; the random content key is wrapped with a Notifications-owned RSA public key using RSA-OAEP and SHA-256. IAM holds only the public key. `key_id` identifies the Notifications private-key version required for decryption.

The authenticated binding string is:

```text
<event_type>:v<schema_version>:<event_id>:<occurred_at RFC3339Nano>:<algorithm>:<key_id>
```

That exact byte string is used as both the RSA-OAEP label when wrapping the content key and the AES-GCM associated data when sealing the payload. A decryptor must supply the same bytes to both operations.

The recipient Email Address, template variables, and one-time challenge material exist only inside the encrypted payload. They must not be added to Kafka headers, log fields, metrics, or traces.
