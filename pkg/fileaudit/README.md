# File audit ownership

`NewWithFiles(fs)` constructs the existing Audit with a borrowed file service.
Construction is inert. Load subscribes to FS operations and starts publication.
The service must be loaded before Audit; file tools and other producers must
start after Audit and stop before it. Declare those dependencies in extension.Set.
Tools continue receiving only FS and their registry, without audit injection.

Close first revokes and drains the owned FS subscription, then drains queued
records. A timeout preserves remaining resources and wraps ErrCloseIncomplete;
retry with a fresh context. Closing Audit does not close the borrowed FS.
Subscriptions returned by Audit.Subscribe belong to downstream consumers.

The synchronous observer creates the existing aop.file.Access and hashes
committed write bytes before their borrowing period ends. It never rereads a
file for its digest and does not queue file content. Disabled collection skips
record construction and hashing. Publication uses the existing bounded queue;
overflow drops observations and is visible in WatchState.

Read Bytes counts actual consumed bytes, including partial consumption before
failure. Write Bytes counts successfully committed content; temporary-file
activity is not another user operation. CREATE is classified using the existing
pre-write destination check, not an atomic claim about concurrent writers.

`New()` still serves existing explicit Record/RecordFile and shell-snapshot
consumers. Those production call sites have not yet migrated. Do not combine
explicit reporting and FS observation for the same operation: it duplicates the
record. Shell snapshots and AOP file operations are outside this increment.
