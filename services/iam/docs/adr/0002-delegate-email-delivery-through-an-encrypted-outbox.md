# Delegate email delivery through an encrypted outbox

Identity and Access owns verification and recovery challenges but delegates email transport to Notifications. It commits an encrypted delivery request to a transactional outbox with the challenge, then a separate relay publishes the producer-owned event to Kafka with at-least-once delivery and a stable event ID for consumer deduplication. This avoids coupling identity transactions to Notifications availability and prevents raw one-time secrets from appearing in plaintext in the database outbox or Kafka.
