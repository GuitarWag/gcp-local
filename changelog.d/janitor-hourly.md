Changed patch
- **The janitor runs every hour.** It ran every 6 hours, so a flagged
  issue or a broken label could block the pipeline for that long. Set
  `FACTORY_JANITOR_EVERY` (seconds) to change it.
