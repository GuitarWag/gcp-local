Fixed patch
- **Factory herdr runner: stage jobs start work again.** pi tabs start with `--approve`, so pi does not wait at a folder trust question. opencode tabs create their session with `opencode run -m <model>` and then open the TUI on it, because the opencode v2 TUI ignores the configured model and picked a model with no valid credentials.
