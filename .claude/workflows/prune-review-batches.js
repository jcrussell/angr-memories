export const meta = {
  name: 'prune-review-batches',
  description: 'Draft pruning verdicts for every batch in a round, then independently peer-review each batch',
  whenToUse: 'After `angr-memories chunk --round <R> [--delta-since ...]` has written rounds/<R>/batch-index.json and per-batch bodies.json/cites.json. Writes manifest.jsonl into each batch dir. Run `angr-memories validate --round <R>` afterward.',
  phases: [
    { title: 'List batches', detail: 'read batch-index.json for the round' },
    { title: 'Draft', detail: 'one agent per batch drafts forget/merge/promote/keep verdicts' },
    { title: 'Peer Review', detail: 'a second, independent agent per batch checks the drafted verdicts' },
  ],
}

const round = args.round
const roundsDir = (args && args.roundsDir) || 'rounds'

const BATCH_LIST_SCHEMA = {
  type: 'object',
  properties: {
    batches: { type: 'array', items: { type: 'string' } },
  },
  required: ['batches'],
}

const DRAFT_SCHEMA = {
  type: 'object',
  properties: {
    batch: { type: 'string' },
    counts: {
      type: 'object',
      properties: {
        forget: { type: 'number' },
        merge: { type: 'number' },
        promote: { type: 'number' },
        keep: { type: 'number' },
      },
      required: ['forget', 'merge', 'promote', 'keep'],
    },
    promoteCandidates: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          key: { type: 'string' },
          promoted_to: { type: 'string' },
          rationale: { type: 'string' },
        },
      },
    },
  },
  required: ['batch', 'counts'],
}

const PEER_SCHEMA = {
  type: 'object',
  properties: {
    batch: { type: 'string' },
    disagreements: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          key: { type: 'string' },
          manifestVerdict: { type: 'string' },
          concern: { type: 'string' },
          suggestedVerdict: { type: 'string' },
        },
        required: ['key', 'manifestVerdict', 'concern'],
      },
    },
  },
  required: ['batch', 'disagreements'],
}

function draftPrompt(batchId) {
  return [
    `You are reviewing pruning batch ${batchId} for angr-memories round ${round}.`,
    `Working directory is an angr-memories git checkout. Read ${roundsDir}/review-instructions.md in full and follow its decision tree EXACTLY (forget/merge/promote/keep). Read ${roundsDir}/mandatory-keep.txt — those keys must always end up verdict "keep" (they may be a merge canonical, never a subordinate or forgotten key).`,
    `Batch inputs: read ${roundsDir}/${round}/batches/${batchId}/bodies.json (every key you must judge) and ${roundsDir}/${round}/batches/${batchId}/cites.json (citation edges — a key with an "in" edge from OUTSIDE this batch is load-bearing, bias keep).`,
    `Also read triage.jsonl at the repo root and use the rows whose "key" matches a key in this batch's bodies.json as supporting signal for tags like closed_only, dup_of_docs, stale_file_ref, cites_other — but verify claims yourself (e.g. actually grep angr's docs/ or CLAUDE.md before trusting dup_of_docs) rather than trusting a tag blindly.`,
    `A live angr checkout is at ~/repos/angr if you need CLAUDE.md, docs/, git log, or ls to check staleness. Read-only bd commands (bd show/bd list) are fine if useful; do NOT run any mutating command, and do not write anything except this batch's manifest.jsonl.`,
    `Write ${roundsDir}/${round}/batches/${batchId}/manifest.jsonl exactly as specified in review-instructions.md: one JSON object per line, EVERY key from bodies.json appearing EXACTLY once, schema {"key","verdict","reason"} plus verdict-specific fields (merge subordinate: "merged_into"; merge canonical: "canonical":true + "merged_body"; promote: "promoted_to" + "body"). merged_into must reference a canonical key inside THIS batch.`,
    `Report back: the batch id, counts per verdict (forget/merge/promote/keep), and any promote candidates (key, promoted_to path, one-line rationale) — promotes should be rare.`,
  ].join('\n\n')
}

function peerReviewPrompt(batchId) {
  return [
    `You are independently peer-reviewing the pruning verdicts already drafted for batch ${batchId} of angr-memories round ${round}. You have NOT seen the original reviewer's reasoning — only their output — so judge the manifest fresh against the same rules.`,
    `Read ${roundsDir}/review-instructions.md in full for the decision tree, and ${roundsDir}/mandatory-keep.txt for the mandatory-keep list.`,
    `Read ${roundsDir}/${round}/batches/${batchId}/bodies.json (the memory bodies), ${roundsDir}/${round}/batches/${batchId}/manifest.jsonl (the verdicts to check), and ${roundsDir}/${round}/batches/${batchId}/cites.json (citation edges).`,
    `Flag a disagreement ONLY when a verdict looks like a clear rule violation, e.g.: a forget/merge verdict on a key with an "in" citation edge from OUTSIDE this batch; a forget/merge verdict on a mandatory-keep key; a merge subordinate whose merged_into target is not a canonical row in THIS batch's own manifest; a forget verdict that doesn't actually match any of review-instructions.md's forget criteria (a)-(f) based on the body text; a forget verdict citing criterion (f) whose reason doesn't name a code location the detail should move to; a promote verdict that doesn't meet ALL the promote criteria (>=800 chars, durable architecture knowledge, fits one allow-listed docs/*.rst, not already covered there). Do not nitpick plausible borderline forget/keep calls.`,
    `Report the batch id and the list of disagreements (empty array if none), each with the key, the manifest's current verdict, your concern, and a suggested verdict.`,
  ].join('\n\n')
}

phase('List batches')
let batchIds
if (args && Array.isArray(args.batchIds) && args.batchIds.length) {
  batchIds = args.batchIds
  log(`Round ${round}: using ${batchIds.length} batch IDs passed via args — ${batchIds.join(', ')}`)
} else {
  const batchList = await agent(
    [
      `Read the JSON file ${roundsDir}/${round}/batch-index.json in the current git repo (an angr-memories checkout).`,
      `It is an object whose top-level keys are batch IDs. Return the list of batch IDs.`,
    ].join('\n\n'),
    { schema: BATCH_LIST_SCHEMA, phase: 'List batches', label: 'list-batches' }
  )
  batchIds = batchList.batches
  log(`Round ${round}: reviewing ${batchIds.length} batches — ${batchIds.join(', ')}`)
}

const reviewed = await pipeline(
  batchIds,
  (batchId) => agent(draftPrompt(batchId), { schema: DRAFT_SCHEMA, phase: 'Draft', label: `draft:${batchId}` }),
  (draft, batchId) =>
    agent(peerReviewPrompt(batchId), { schema: PEER_SCHEMA, phase: 'Peer Review', label: `peer:${batchId}` }).then(
      (peer) => ({ batch: batchId, draft, peer })
    )
)

const batches = reviewed.filter(Boolean)
const totalDisagreements = batches.reduce((n, b) => n + (b.peer?.disagreements?.length || 0), 0)
log(`Done: ${batches.length}/${batchIds.length} batches reviewed, ${totalDisagreements} peer-review disagreement(s) flagged`)

return { round, batches }
