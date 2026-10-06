# Security

## Reporting a problem

Please don't open a public issue for security problems. Use GitHub's
**Security → Report a vulnerability** on this repository (private
vulnerability reporting), or contact the maintainers directly.

## What must never be in this repository

The app keeps its data next to the data file (default `lotto.json`):

| File | Contains |
|---|---|
| `lotto.json` | users and password hashes, agents, tickets, prices, results, setup |
| `lotto-activity.jsonl` | the activity log, with names and IP addresses |
| `lotto-documents/` | scans of people's ID documents and agents' legal papers |
| `lotto-logo` | the uploaded logo |

`.gitignore` and the pre-commit hook in `.githooks/` keep them out, and the
CI fails if any are committed. Back them up somewhere private instead.

If data or a secret was ever pushed, deleting the file in a new commit is
not enough: it stays in the history. Reset every affected password, revoke
the token, and remove it from history (e.g. `git filter-repo`) before
pushing again.
