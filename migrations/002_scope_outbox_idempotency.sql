ALTER TABLE async_outbox
  DROP INDEX uniq_async_outbox_idempotency,
  ADD UNIQUE KEY uniq_async_outbox_idempotency (kind, idempotency_key);
