Changed patch
- **Gate slots.** `scripts/ci/gate.sh` runs at most 2 at the same time
  on one machine (`GATE_SLOTS`). Other runs wait for a free slot. Each
  run can use 1 to 2 GB, and parallel factory agents ran out of memory.
