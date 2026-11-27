Changed patch
- **Factory: faster and even agent use.** `FACTORY_AGENTS` entries can name a model (`pi:yld/glm-5.3-flash`), so jobs rotate harness and model pairs. Job slots go to the latest stage first. herdr jobs that make progress renew their claim, so a slow agent no longer fails at the 60-minute lease.
