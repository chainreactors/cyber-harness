# Filesystem resource extension

`files.Extension` owns an `os.Root`, path policy and admitted operations. It implements
Load/Close directly and has no tool, profile, audit or Agent dependency. Observer
operations reuse the existing AOP AccessOp enum; no protocol runtime is installed.

`files.New(files.Config{Directory: absoluteWorkDir, ReadOnly: false, MaxBytes: 1 << 20})`
only validates configuration. Load opens an existing directory; it does not
create one. Its context bounds initialization, not the resource lifetime. Failed
Load seals the instance; finish cleanup with Close and construct a new FS.

Read returns owned bytes. Write borrows bytes until return. Both accept local
paths, enforce the byte limit and reject nonregular files. ReadOnly is enforced
by FS, including direct calls without tools. FS accepts binary bytes; encoding
and presentation belong to consumers.

Write uses a sibling temporary file and rename, preserving existing permissions
and using 0600 for new files. It rejects a symlink destination. Failure before
rename preserves the original and removes the temporary file. Parent directories
are not created. Cancellation is checked between IO chunks and before rename;
syscalls cannot be preempted or successful renames undone. There is no fsync or
cross-platform atomic-rename guarantee.

Close stops admission, requests cancellation and waits before closing the root.
A timeout retains the handle; retry Close with a fresh context. Closed instances
cannot reload. Ready reports availability but does not grant a resource lease.
Incomplete waits wrap extension.ErrCloseIncomplete and the context error. Closing
the root consumes the handle even if it reports an error; subsequent Close does
not retry that handle. Shared lifecycle, subscription and operation types come
from core/extension, core/eventbus and aop/file.

Profiles declare FS as a dependency of any extension borrowing it. File tool Close
unregisters tools without closing FS, so non-tool consumers remain usable.

Subscribe provides synchronous operation callbacks using the existing
eventbus.Subscription admission and drain. Observers own their subscriptions;
callbacks finish before FS releases the operation. Data is immutable and borrowed
until the callback returns. An observer retaining data must copy it. FS does not
compute digests, create audit records, queue content or depend on FileAudit.

Read reports actual consumed bytes, including bytes consumed before an error.
Write reports committed content once; internal temporary files produce no events.
CREATE reflects the destination's absence at the existing pre-write Lstat; it is
not an atomic create-only guarantee against concurrent writers. Failed writes
report an error and unknown destination size/bytes, without a success digest.
Admission rejected before an operation starts produces no observation.

This package is the sole bounded filesystem resource owner. It is not a tool
group and it is not a workspace profile. Extended product file

| Package | Role | Owns |
| --- | --- | --- |
| `pkg/files` | resource extension | `os.Root`, path policy, in-flight file operations |
| `pkg/files` | file extension | read/write/ls/glob tools and filesystem lifecycle |
| `pkg/toolset/workspacefiles` | host workspace tool extension | workdir and virtual-source registrations |
| `pkg/profile/workspace` | profile assembler | creates one Set and orders the extensions |

`filetools` and `workspacefiles` intentionally expose different path policies;
they are not duplicate resource owners. A profile selects them explicitly and
assigns distinct owner IDs.
behavior, mounts and the AOP file namespace remain to be merged.
