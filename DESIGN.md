# Build trigger diagnostics

The engine is the single owner of build/run state (see README's Design).
The watcher sends `WatchEvent` values containing a relative path and the
observed fsnotify operation bits. The debouncer deduplicates identical
path/operation pairs in first-observed order within a fixed window. Backoff
merging uses the same identity, preserving different operations on one path.

A zero-operation event denotes an internal reason: startup, automatic retry,
or event overflow recovery. Actual filesystem events always carry operations,
so a file named `startup` is distinguishable from startup itself. Overflow
has no known path and must never be presented as a specific file change.

The engine retains the complete formatted trigger list for build failure
reports and a five-event summary for build and restart lifecycle logs.
`-verbose` emits the full deduplicated batch before each build. Quote paths
to prevent spaces, delimiters, or control characters from hiding event boundaries.
Diagnostics describe observations, not content changes or writer identity.
