# MumuBot Reference Refactor

## Target

The bot is a continuously present group member. Every message becomes a fact;
speaking is an optional, delayed consequence of that fact. This replaces a
per-message request/response loop with a durable presence runtime.

## Reference Patterns To Reuse

MumuBot informs four bounded patterns:

1. Treat `image/sub_type=1` and `mface` as stickers, then inject a vision or
   platform-summary descriptor into context.
2. Collect stickers asynchronously, search by descriptor, send by stable ID,
   and write the bot's own send back into conversation history.
3. Batch topic projection and reflection outside the ingress/actor path.
4. Advance a durable learning watermark after a successful batch rather than
   repeatedly scanning a rolling message window.

## Delivery Plan

### Phase 1: Media Perception

```text
Inbound event -> Actor fact -> background perception
-> descriptor fallback -> Actor enrichment -> sticker metadata/indexing
```

Media work must not block ingress. The actor owns enrichment; workers submit
results only. Generic images remain conversational context and do not enter the
sticker pool.

### Phase 2: Topic Projection

Consume archived facts in batches to assign an existing topic, create a new
topic, or mark no topic. Persist a versioned summary with participants, open
loops, recent turns, and source events. Keep only the active pointer and short
tail in the actor.

### Phase 3: Evidence-Backed Learning

Maintain a `(group_id, projector_kind)` watermark. A learning candidate must
carry evidence IDs through review to the published memory. Never re-learn facts
before a successfully committed watermark.

## Current Implementation

- Stable OneBot event IDs and idempotent archive-before-consume writes make
  message retries safe.
- A new message in the same burst cancels prior candidates. Claimed candidates
  are validated before deliberation and again before sending.
- Planner action proposals are constrained by the candidate's allowed
  expression modes, then become the executor's sole action authority.
- Turn outcomes update durable cooldown/topic state and persona state before a
  later scheduler claim.
- Context merges the durable archive with the process-local tail after restart
  and projects the active topic and open loops into the prompt.
- Learning advances a durable `learning_extract` watermark after successful
  review. Its primary evidence event is retained in the emitted memory record.

## Remaining Work

- Persist all evidence IDs on a memory record, rather than its primary source
  event only.
- Implement the persistent topic projector and replay tests.
- Download collected sticker binaries into managed object storage and make
  failed media jobs durable and retryable.

## Deliberate Non-Transfers

- No synchronous agent call in the ingress path.
- No direct model mutation of mood or relationship state.
- No local asset path exposed to the model; sticker use goes through stable IDs.
