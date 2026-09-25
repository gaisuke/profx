# Evaluation harness

Unit tests answer "does the code do what I wrote?". This harness answers a
different question: **does the model actually score the way the rubric says?**

```bash
EVAL_ENABLE=1 OPENCODE_GO_API_KEY=... go test -tags eval ./internal/services/ -run TestEvalHarness -v
```

It is guarded twice — a `eval` build tag **and** `EVAL_ENABLE=1` — so ordinary
`go test ./...` never reaches the network.

## What it measures

- **Agreement**: how often the CV match rate and the project score land inside the
  band the case's construction implies (strong / partial / mismatch).
- **Mean absolute error** against the band midpoint: a model that is right on
  average but noisy looks different from one that is consistently too generous.
- **Hard failures**: unparseable replies, out-of-range scores, provider errors —
  the structured-output reliability that decides whether a pipeline is shippable.
- **Drift**: with `EVAL_REPEAT=3`, the largest spread of the same case across runs
  at a fixed temperature of 0.2.
- **Latency** per case (three stages), for cost and timeout budgeting.

## Design choices worth knowing

- **The rubric is pinned in the case file**, not fetched from Ragie. Run-to-run
  movement therefore comes from the model, never from retrieval variance.
  Retrieval quality is a separate question and needs its own harness.
- **Labels are constructed, not human-annotated.** Each case is written so the
  intended band follows from the rubric by construction (a CV that lists every
  must-have with measured impact is "strong" by definition). This is weaker than
  a gold-standard set annotated by a recruiter; treat the numbers as a smoke test
  for scoring behaviour and a drift alarm, not as accuracy against ground truth.
  Replace or extend the cases with your own judgement to raise the bar.
- **Chat history is absent on purpose**: each stage is a single stateless call, so
  a case cannot leak context into the next.

## Files

- `cases/*.json` — one case per file: job title, rubric, CV text, report text and
  the expected bands.
- `out/report.json` — machine-readable results (git-ignored).
- `out/report.md` — the same, as a table with the produced summaries.

## Reading a run

```
CV in band: 4/6 | CV MAE 0.083
project in band: 5/6 | project MAE 0.350
hard failures: 0 | mean latency 15200ms | max CV spread 0.05
```

`CV in band 4/6` with `0 hard failures` means the model understood the rubric but
disagreed with the case construction twice; open `report.md` and read the feedback
column to see whether the model or the case is the one that is wrong.
