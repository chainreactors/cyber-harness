# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [aop/protocol.proto](#aop_protocol-proto)
    - [AgentAccepted](#aop-AgentAccepted)
    - [AgentHello](#aop-AgentHello)
    - [AgentRuntimeInfo](#aop-AgentRuntimeInfo)
    - [AgentStats](#aop-AgentStats)
    - [AgentStatus](#aop-AgentStatus)
    - [CancelOperation](#aop-CancelOperation)
    - [ProtocolMessage](#aop-ProtocolMessage)

- [aop/exec/protocol.proto](#aop_exec_protocol-proto)
    - [Output](#aop-exec-Output)
    - [ProtocolMessage](#aop-exec-ProtocolMessage)
    - [Request](#aop-exec-Request)
    - [Request.EnvEntry](#aop-exec-Request-EnvEntry)
    - [Result](#aop-exec-Result)

    - [Stream](#aop-exec-Stream)

- [aop/file/protocol.proto](#aop_file_protocol-proto)
    - [Access](#aop-file-Access)
    - [Entry](#aop-file-Entry)
    - [ListRequest](#aop-file-ListRequest)
    - [MkdirRequest](#aop-file-MkdirRequest)
    - [ProtocolMessage](#aop-file-ProtocolMessage)
    - [ReadRequest](#aop-file-ReadRequest)
    - [Result](#aop-file-Result)
    - [UploadRequest](#aop-file-UploadRequest)
    - [WriteRequest](#aop-file-WriteRequest)

    - [AccessOp](#aop-file-AccessOp)
    - [AccessSource](#aop-file-AccessSource)

- [aop/operation/protocol.proto](#aop_operation_protocol-proto)
    - [Completed](#aop-operation-Completed)
    - [Decision](#aop-operation-Decision)
    - [Failure](#aop-operation-Failure)
    - [Ref](#aop-operation-Ref)
    - [Started](#aop-operation-Started)

    - [Correlation](#aop-operation-Correlation)
    - [DecisionAction](#aop-operation-DecisionAction)
    - [FailureKind](#aop-operation-FailureKind)

- [aop/pty/protocol.proto](#aop_pty_protocol-proto)
    - [Attach](#aop-pty-Attach)
    - [Attached](#aop-pty-Attached)
    - [Close](#aop-pty-Close)
    - [Closed](#aop-pty-Closed)
    - [Detach](#aop-pty-Detach)
    - [Detached](#aop-pty-Detached)
    - [Error](#aop-pty-Error)
    - [Input](#aop-pty-Input)
    - [Kill](#aop-pty-Kill)
    - [List](#aop-pty-List)
    - [Open](#aop-pty-Open)
    - [Opened](#aop-pty-Opened)
    - [Output](#aop-pty-Output)
    - [ProtocolMessage](#aop-pty-ProtocolMessage)
    - [Resize](#aop-pty-Resize)
    - [Session](#aop-pty-Session)
    - [Sessions](#aop-pty-Sessions)
    - [State](#aop-pty-State)

- [aop/sco/protocol.proto](#aop_sco_protocol-proto)
    - [Nodes](#aop-sco-Nodes)
    - [ProtocolMessage](#aop-sco-ProtocolMessage)

- [aop/tool/protocol.proto](#aop_tool_protocol-proto)
    - [Artifact](#aop-tool-Artifact)
    - [Call](#aop-tool-Call)
    - [Loot](#aop-tool-Loot)
    - [Progress](#aop-tool-Progress)
    - [ProtocolMessage](#aop-tool-ProtocolMessage)

- [aop/traffic/protocol.proto](#aop_traffic_protocol-proto)
    - [CaptureConfig](#aop-traffic-CaptureConfig)
    - [CaptureState](#aop-traffic-CaptureState)
    - [Configure](#aop-traffic-Configure)
    - [Flow](#aop-traffic-Flow)
    - [FlowFilter](#aop-traffic-FlowFilter)
    - [FlowRecord](#aop-traffic-FlowRecord)
    - [Header](#aop-traffic-Header)
    - [HttpRequest](#aop-traffic-HttpRequest)
    - [HttpResponse](#aop-traffic-HttpResponse)
    - [ProtocolMessage](#aop-traffic-ProtocolMessage)
    - [Query](#aop-traffic-Query)
    - [RoutingConfig](#aop-traffic-RoutingConfig)
    - [RoutingState](#aop-traffic-RoutingState)
    - [State](#aop-traffic-State)

    - [CaptureMode](#aop-traffic-CaptureMode)
    - [RoutingMode](#aop-traffic-RoutingMode)

- [Scalar Value Types](#scalar-value-types)



<a name="aop_protocol-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/protocol.proto



<a name="aop-AgentAccepted"></a>

### AgentAccepted



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| node_id | [string](#string) |  |  |
| capabilities | [string](#string) | repeated |  |






<a name="aop-AgentHello"></a>

### AgentHello



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| node_id | [string](#string) |  |  |
| name | [string](#string) |  |  |
| capabilities | [string](#string) | repeated |  |
| tools | [ToolDefinition](#aop-ToolDefinition) | repeated |  |
| runtime | [AgentRuntimeInfo](#aop-AgentRuntimeInfo) |  |  |






<a name="aop-AgentRuntimeInfo"></a>

### AgentRuntimeInfo



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| hostname | [string](#string) |  |  |
| username | [string](#string) |  |  |
| working_dir | [string](#string) |  |  |
| os | [string](#string) |  |  |
| arch | [string](#string) |  |  |
| pid | [int32](#int32) |  |  |
| metadata | [google.protobuf.Struct](#google-protobuf-Struct) |  |  |






<a name="aop-AgentStats"></a>

### AgentStats



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| turns | [uint64](#uint64) |  |  |
| tool_calls | [uint64](#uint64) |  |  |
| running_tools | [uint64](#uint64) |  |  |
| input_tokens | [uint64](#uint64) |  |  |
| output_tokens | [uint64](#uint64) |  |  |
| total_tokens | [uint64](#uint64) |  |  |
| cache_read_tokens | [uint64](#uint64) |  |  |
| cache_write_tokens | [uint64](#uint64) |  |  |
| last_event | [string](#string) |  |  |






<a name="aop-AgentStatus"></a>

### AgentStatus



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| provider | [string](#string) |  |  |
| model | [string](#string) |  |  |
| space | [string](#string) |  |  |
| bound | [bool](#bool) |  |  |
| config_error | [string](#string) |  |  |






<a name="aop-CancelOperation"></a>

### CancelOperation



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| target_id | [string](#string) |  |  |
| reason | [string](#string) |  |  |






<a name="aop-ProtocolMessage"></a>

### ProtocolMessage
ProtocolMessage is the typed union for the AOP core namespace. Extension
packages define their own ProtocolMessage and do not modify this one.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| agent_hello | [AgentHello](#aop-AgentHello) |  |  |
| agent_accepted | [AgentAccepted](#aop-AgentAccepted) |  |  |
| agent_status | [AgentStatus](#aop-AgentStatus) |  |  |
| agent_stats | [AgentStats](#aop-AgentStats) |  |  |
| open_session_request | [OpenSessionRequest](#aop-OpenSessionRequest) |  |  |
| open_session_response | [OpenSessionResponse](#aop-OpenSessionResponse) |  |  |
| run_turn_request | [RunTurnRequest](#aop-RunTurnRequest) |  |  |
| run_turn_response | [RunTurnResponse](#aop-RunTurnResponse) |  |  |
| cancel_turn_request | [CancelTurnRequest](#aop-CancelTurnRequest) |  |  |
| cancel_turn_response | [CancelTurnResponse](#aop-CancelTurnResponse) |  |  |
| close_session_request | [CloseSessionRequest](#aop-CloseSessionRequest) |  |  |
| close_session_response | [CloseSessionResponse](#aop-CloseSessionResponse) |  |  |
| watch_events_request | [WatchEventsRequest](#aop-WatchEventsRequest) |  |  |
| list_events_request | [ListEventsRequest](#aop-ListEventsRequest) |  |  |
| list_events_response | [ListEventsResponse](#aop-ListEventsResponse) |  |  |
| event | [Event](#aop-Event) |  |  |
| cancel_operation | [CancelOperation](#aop-CancelOperation) |  |  |
| protocol_error | [ProtocolError](#aop-ProtocolError) |  |  |















<a name="aop_exec_protocol-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/exec/protocol.proto



<a name="aop-exec-Output"></a>

### Output



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream | [Stream](#aop-exec-Stream) |  |  |
| data | [bytes](#bytes) |  |  |






<a name="aop-exec-ProtocolMessage"></a>

### ProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request | [Request](#aop-exec-Request) |  |  |
| output | [Output](#aop-exec-Output) |  |  |
| result | [Result](#aop-exec-Result) |  |  |






<a name="aop-exec-Request"></a>

### Request



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| command | [string](#string) |  |  |
| cwd | [string](#string) |  |  |
| timeout_seconds | [uint32](#uint32) |  |  |
| env | [Request.EnvEntry](#aop-exec-Request-EnvEntry) | repeated |  |






<a name="aop-exec-Request-EnvEntry"></a>

### Request.EnvEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |






<a name="aop-exec-Result"></a>

### Result



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| exit_code | [int32](#int32) |  |  |
| state | [string](#string) |  |  |
| kill_cause | [string](#string) |  |  |








<a name="aop-exec-Stream"></a>

### Stream


| Name | Number | Description |
| ---- | ------ | ----------- |
| STREAM_UNSPECIFIED | 0 |  |
| STREAM_STDOUT | 1 |  |
| STREAM_STDERR | 2 |  |










<a name="aop_file_protocol-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/file/protocol.proto



<a name="aop-file-Access"></a>

### Access
Access is one observed file access.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| op | [AccessOp](#aop-file-AccessOp) |  |  |
| source | [AccessSource](#aop-file-AccessSource) |  |  |
| path | [string](#string) |  | path is absolute; work_dir is the execution&#39;s working directory, carried so a consumer can present the path relative to it without guessing. |
| work_dir | [string](#string) |  |  |
| size | [int64](#int64) |  | file size after the access |
| bytes | [int64](#int64) |  | bytes read or written by this access, 0 when unknown |
| edits | [uint32](#uint32) |  | patch count for EDIT |
| digest | [string](#string) |  | sha256 of the content after a write, when computed |
| error | [string](#string) |  |  |
| timestamp | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="aop-file-Entry"></a>

### Entry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| is_directory | [bool](#bool) |  |  |
| size | [int64](#int64) |  |  |






<a name="aop-file-ListRequest"></a>

### ListRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| path | [string](#string) |  |  |






<a name="aop-file-MkdirRequest"></a>

### MkdirRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| path | [string](#string) |  |  |






<a name="aop-file-ProtocolMessage"></a>

### ProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| read_request | [ReadRequest](#aop-file-ReadRequest) |  |  |
| write_request | [WriteRequest](#aop-file-WriteRequest) |  |  |
| list_request | [ListRequest](#aop-file-ListRequest) |  |  |
| mkdir_request | [MkdirRequest](#aop-file-MkdirRequest) |  |  |
| upload_request | [UploadRequest](#aop-file-UploadRequest) |  |  |
| result | [Result](#aop-file-Result) |  |  |






<a name="aop-file-ReadRequest"></a>

### ReadRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| path | [string](#string) |  |  |
| offset | [int64](#int64) |  | offset and limit enable bounded reads for large artifacts. A zero limit preserves the original whole-file behavior for older clients. |
| limit | [int32](#int32) |  |  |






<a name="aop-file-Result"></a>

### Result



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| path | [string](#string) |  |  |
| filename | [string](#string) |  |  |
| size | [int64](#int64) |  |  |
| data | [bytes](#bytes) |  |  |
| entries | [Entry](#aop-file-Entry) | repeated |  |
| media_type | [string](#string) |  |  |
| offset | [int64](#int64) |  | offset is the position of data within the file; size remains the total file size. eof marks the final chunk. |
| eof | [bool](#bool) |  |  |






<a name="aop-file-UploadRequest"></a>

### UploadRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| filename | [string](#string) |  |  |
| media_type | [string](#string) |  |  |
| data | [bytes](#bytes) |  |  |






<a name="aop-file-WriteRequest"></a>

### WriteRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| path | [string](#string) |  |  |
| data | [bytes](#bytes) |  |  |








<a name="aop-file-AccessOp"></a>

### AccessOp
AccessOp is what happened to the path. EDIT is a targeted patch and WRITE a
full-content overwrite; both are distinguished from CREATE, which says the
path did not exist beforehand.

| Name | Number | Description |
| ---- | ------ | ----------- |
| ACCESS_OP_UNSPECIFIED | 0 |  |
| ACCESS_OP_READ | 1 |  |
| ACCESS_OP_WRITE | 2 |  |
| ACCESS_OP_EDIT | 3 |  |
| ACCESS_OP_CREATE | 4 |  |
| ACCESS_OP_DELETE | 5 |  |



<a name="aop-file-AccessSource"></a>

### AccessSource
AccessSource is how the access was observed, which is also how far it can be
trusted. TOOL is an exact record taken inside the tool that performed it.
SNAPSHOT is derived by diffing the work dir around a shell execution: the
path and the operation are real, but attribution to that execution is an
inference, and reads are invisible to it entirely. CONTROL is a file request
this node served for a peer rather than anything the agent did.

| Name | Number | Description |
| ---- | ------ | ----------- |
| ACCESS_SOURCE_UNSPECIFIED | 0 |  |
| ACCESS_SOURCE_TOOL | 1 |  |
| ACCESS_SOURCE_SNAPSHOT | 2 |  |
| ACCESS_SOURCE_CONTROL | 3 |  |










<a name="aop_operation_protocol-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/operation/protocol.proto



<a name="aop-operation-Completed"></a>

### Completed



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| kind | [string](#string) |  |  |
| name | [string](#string) |  |  |
| started_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | Absent means the underlying operation never started (denial, cancellation before admission, or a start failure). |
| failure | [Failure](#aop-operation-Failure) |  | Absent means the execution boundary returned normally. Domain success is still defined by ToolResult, CommandResult or the native process state. |






<a name="aop-operation-Decision"></a>

### Decision
Decision is the common observation shape for a policy decision. Policy-specific
rationale is carried as a typed Event extension owned by that policy.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| point | [string](#string) |  |  |
| policy | [string](#string) |  |  |
| action | [DecisionAction](#aop-operation-DecisionAction) |  |  |
| failure | [Failure](#aop-operation-Failure) |  |  |






<a name="aop-operation-Failure"></a>

### Failure



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| kind | [FailureKind](#aop-operation-FailureKind) |  |  |
| message | [string](#string) |  |  |






<a name="aop-operation-Ref"></a>

### Ref
Ref is the single wire authority for execution correlation. SessionID,
TurnID and emitter remain on the enclosing aop.Event.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| call_id | [string](#string) |  |  |
| operation_id | [string](#string) |  |  |
| parent_operation_id | [string](#string) |  |  |
| resource_id | [string](#string) |  |  |
| correlation | [Correlation](#aop-operation-Correlation) |  |  |






<a name="aop-operation-Started"></a>

### Started
Started and Completed are open AOP extension payloads. kind is a stable,
extension-defined identifier such as &#34;tool&#34;, &#34;command&#34; or &#34;process&#34;; it is
not a closed enum. Event.emitted_at is the actual transition timestamp.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| kind | [string](#string) |  |  |
| name | [string](#string) |  |  |








<a name="aop-operation-Correlation"></a>

### Correlation
Correlation reports whether the source carried a trustworthy operation
identity. An explicitly created root operation remains EXPLICIT even when it
has no SessionID or CallID. UNATTRIBUTED means the source could not resolve
the origin and must never be rebound to a newer call.

| Name | Number | Description |
| ---- | ------ | ----------- |
| CORRELATION_UNSPECIFIED | 0 |  |
| CORRELATION_EXPLICIT | 1 |  |
| CORRELATION_UNATTRIBUTED | 2 |  |



<a name="aop-operation-DecisionAction"></a>

### DecisionAction


| Name | Number | Description |
| ---- | ------ | ----------- |
| DECISION_ACTION_UNSPECIFIED | 0 |  |
| DECISION_ACTION_ALLOW | 1 |  |
| DECISION_ACTION_DENY | 2 |  |
| DECISION_ACTION_CANCEL | 3 |  |



<a name="aop-operation-FailureKind"></a>

### FailureKind
FailureKind is deliberately small. Extensions express narrower semantics as
their own typed messages in aop.Event.extensions instead of extending a
central outcome taxonomy.

| Name | Number | Description |
| ---- | ------ | ----------- |
| FAILURE_KIND_UNSPECIFIED | 0 |  |
| FAILURE_KIND_ERROR | 1 |  |
| FAILURE_KIND_DENIED | 2 |  |
| FAILURE_KIND_START_FAILED | 3 |  |
| FAILURE_KIND_CANCELED | 4 |  |
| FAILURE_KIND_TIMEOUT | 5 |  |
| FAILURE_KIND_PANIC | 6 |  |










<a name="aop_pty_protocol-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/pty/protocol.proto



<a name="aop-pty-Attach"></a>

### Attach



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| session_id | [string](#string) |  |  |
| cols | [int32](#int32) |  |  |
| rows | [int32](#int32) |  |  |






<a name="aop-pty-Attached"></a>

### Attached



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| session | [Session](#aop-pty-Session) |  |  |






<a name="aop-pty-Close"></a>

### Close



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |






<a name="aop-pty-Closed"></a>

### Closed



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| session | [Session](#aop-pty-Session) |  |  |






<a name="aop-pty-Detach"></a>

### Detach



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |






<a name="aop-pty-Detached"></a>

### Detached



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |






<a name="aop-pty-Error"></a>

### Error



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| message | [string](#string) |  |  |






<a name="aop-pty-Input"></a>

### Input



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| data | [bytes](#bytes) |  |  |






<a name="aop-pty-Kill"></a>

### Kill



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |






<a name="aop-pty-List"></a>

### List



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| node_id | [string](#string) |  |  |






<a name="aop-pty-Open"></a>

### Open



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| node_id | [string](#string) |  |  |
| kind | [string](#string) |  |  |
| name | [string](#string) |  |  |
| command | [string](#string) |  |  |
| args | [string](#string) | repeated |  |
| cols | [int32](#int32) |  |  |
| rows | [int32](#int32) |  |  |
| singleton | [bool](#bool) |  |  |






<a name="aop-pty-Opened"></a>

### Opened



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| session | [Session](#aop-pty-Session) |  |  |






<a name="aop-pty-Output"></a>

### Output



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| data | [bytes](#bytes) |  |  |
| offset | [int64](#int64) |  |  |






<a name="aop-pty-ProtocolMessage"></a>

### ProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| open | [Open](#aop-pty-Open) |  |  |
| input | [Input](#aop-pty-Input) |  |  |
| output | [Output](#aop-pty-Output) |  |  |
| resize | [Resize](#aop-pty-Resize) |  |  |
| list | [List](#aop-pty-List) |  |  |
| sessions | [Sessions](#aop-pty-Sessions) |  |  |
| attach | [Attach](#aop-pty-Attach) |  |  |
| detach | [Detach](#aop-pty-Detach) |  |  |
| close | [Close](#aop-pty-Close) |  |  |
| state | [State](#aop-pty-State) |  |  |
| error | [Error](#aop-pty-Error) |  |  |
| opened | [Opened](#aop-pty-Opened) |  |  |
| attached | [Attached](#aop-pty-Attached) |  |  |
| detached | [Detached](#aop-pty-Detached) |  |  |
| kill | [Kill](#aop-pty-Kill) |  |  |
| closed | [Closed](#aop-pty-Closed) |  |  |






<a name="aop-pty-Resize"></a>

### Resize



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| cols | [int32](#int32) |  |  |
| rows | [int32](#int32) |  |  |






<a name="aop-pty-Session"></a>

### Session



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| kind | [string](#string) |  |  |
| name | [string](#string) |  |  |
| command | [string](#string) |  |  |
| pid | [int32](#int32) |  |  |
| started_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| last_activity_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| ended_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| activity_seq | [int64](#int64) |  |  |
| output_bytes | [int64](#int64) |  |  |
| exit_code | [int32](#int32) |  |  |
| state | [string](#string) |  |  |
| kill_cause | [string](#string) |  |  |






<a name="aop-pty-Sessions"></a>

### Sessions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| sessions | [Session](#aop-pty-Session) | repeated |  |






<a name="aop-pty-State"></a>

### State



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stream_id | [string](#string) |  |  |
| session | [Session](#aop-pty-Session) |  |  |















<a name="aop_sco_protocol-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/sco/protocol.proto



<a name="aop-sco-Nodes"></a>

### Nodes
Nodes carries libcstx-owned node documents without copying the libcstx
schema into AOP. Each entry uses the declared media type.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| nodes | [bytes](#bytes) | repeated |  |
| media_type | [string](#string) |  |  |






<a name="aop-sco-ProtocolMessage"></a>

### ProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| nodes | [Nodes](#aop-sco-Nodes) |  |  |















<a name="aop_tool_protocol-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/tool/protocol.proto



<a name="aop-tool-Artifact"></a>

### Artifact
Artifact carries one scanner-native structured record. Nodes remain thin:
only the server normalizes these records into canonical SCO documents.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tool | [string](#string) |  |  |
| kind | [string](#string) |  |  |
| target | [string](#string) |  |  |
| data | [bytes](#bytes) |  |  |
| media_type | [string](#string) |  |  |
| timestamp | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| result_id | [string](#string) |  |  |






<a name="aop-tool-Call"></a>

### Call



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| turn_id | [string](#string) |  |  |
| call | [aop.ToolCall](#aop-ToolCall) |  |  |






<a name="aop-tool-Loot"></a>

### Loot
Loot marks a scanner-native artifact as valuable without replacing or
duplicating the observed artifact. result_id joins the marker to Artifact.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| result_id | [string](#string) |  |  |
| tool | [string](#string) |  |  |
| kind | [string](#string) |  |  |
| target | [string](#string) |  |  |
| priority | [string](#string) |  |  |
| tags | [string](#string) | repeated |  |
| description | [string](#string) |  |  |
| verification_status | [string](#string) |  |  |






<a name="aop-tool-Progress"></a>

### Progress



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tool | [string](#string) |  |  |
| target | [string](#string) |  |  |
| timestamp | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| text | [string](#string) |  |  |
| call_id | [string](#string) |  |  |






<a name="aop-tool-ProtocolMessage"></a>

### ProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| progress | [Progress](#aop-tool-Progress) |  |  |
| call | [Call](#aop-tool-Call) |  |  |















<a name="aop_traffic_protocol-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/traffic/protocol.proto



<a name="aop-traffic-CaptureConfig"></a>

### CaptureConfig
CaptureConfig sets the hub&#39;s capture behaviour. It flips the runtime record
flag; the listener address never changes so in-flight children are unaffected.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| mode | [CaptureMode](#aop-traffic-CaptureMode) |  |  |
| decrypt_https | [bool](#bool) |  | intercept CONNECT to MITM-decrypt HTTPS |
| filter | [FlowFilter](#aop-traffic-FlowFilter) |  | record only matching flows |






<a name="aop-traffic-CaptureState"></a>

### CaptureState



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| mode | [CaptureMode](#aop-traffic-CaptureMode) |  |  |
| capturing | [bool](#bool) |  |  |






<a name="aop-traffic-Configure"></a>

### Configure
Configure declares desired routing and/or capture state. An absent sub-message
leaves that facet unchanged; the handler replies with the resulting State.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| routing | [RoutingConfig](#aop-traffic-RoutingConfig) |  |  |
| capture | [CaptureConfig](#aop-traffic-CaptureConfig) |  |  |






<a name="aop-traffic-Flow"></a>

### Flow
Flow is one captured request/response exchange. Its nested shape mirrors the
consumer&#39;s http.exchange form so a consumer can map it directly. Correlation
is carried once by aop.operation.Ref on the containing AOP Event. Fields 2-11
were the former embedded correlation and pre-nesting flat shape.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| error | [string](#string) |  |  |
| complete | [bool](#bool) |  |  |
| timestamp | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| request | [HttpRequest](#aop-traffic-HttpRequest) |  |  |
| response | [HttpResponse](#aop-traffic-HttpResponse) |  | absent when no response was received |






<a name="aop-traffic-FlowFilter"></a>

### FlowFilter
FlowFilter bounds which flows are recorded (CaptureConfig) or returned (Query).


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| host | [string](#string) |  | host substring |
| status | [string](#string) |  | status class or code, e.g. &#34;2xx&#34;, &#34;404&#34;, &#34;5xx&#34; |
| type | [string](#string) |  | Content-Type substring |
| last | [uint32](#uint32) |  | return only the last N flows (Query) |






<a name="aop-traffic-FlowRecord"></a>

### FlowRecord
FlowRecord is the resource-query representation. Live observations use the
same Flow as Event.extension and carry this Ref in Event.extensions.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| operation | [aop.operation.Ref](#aop-operation-Ref) |  |  |
| flow | [Flow](#aop-traffic-Flow) |  |  |






<a name="aop-traffic-Header"></a>

### Header



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| value | [string](#string) |  |  |






<a name="aop-traffic-HttpRequest"></a>

### HttpRequest
HttpRequest is the request half of an exchange.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| method | [string](#string) |  |  |
| url | [string](#string) |  |  |
| protocol | [string](#string) |  |  |
| headers | [Header](#aop-traffic-Header) | repeated |  |
| body | [bytes](#bytes) |  |  |






<a name="aop-traffic-HttpResponse"></a>

### HttpResponse
HttpResponse is the response half of an exchange. It is optional on Flow: a
request that never got a response (timeout, refused connection, one-way
capture) has no response half.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| status_code | [int32](#int32) |  |  |
| reason_phrase | [string](#string) |  |  |
| headers | [Header](#aop-traffic-Header) | repeated |  |
| body | [bytes](#bytes) |  |  |






<a name="aop-traffic-ProtocolMessage"></a>

### ProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| configure | [Configure](#aop-traffic-Configure) |  |  |
| query | [Query](#aop-traffic-Query) |  |  |
| state | [State](#aop-traffic-State) |  |  |
| flow_record | [FlowRecord](#aop-traffic-FlowRecord) |  |  |






<a name="aop-traffic-Query"></a>

### Query
Query requests a snapshot: the current State and/or the recorded flows.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| state | [bool](#bool) |  | request current State |
| flows | [bool](#bool) |  | request recorded flows (batched Flow replies) |
| filter | [FlowFilter](#aop-traffic-FlowFilter) |  | filter for flows = true |






<a name="aop-traffic-RoutingConfig"></a>

### RoutingConfig
RoutingConfig steers the egress chain (State in tools/proxy). Fields beyond
mode/url/selector are the auto-mode subscription filters.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| mode | [RoutingMode](#aop-traffic-RoutingMode) |  |  |
| url | [string](#string) |  | proxy URL (PROXY) or subscription URL (SUBSCRIBE/AUTO) |
| selector | [string](#string) |  | node name or 1-based index (SWITCH) |
| type | [string](#string) |  | protocol filter, e.g. &#34;trojan,vless&#34; (AUTO) |
| name | [string](#string) |  | node name keyword (AUTO) |
| country | [string](#string) |  | ISO 3166-1 alpha-2 filter, e.g. &#34;HK,JP&#34; (AUTO) |
| strategy | [string](#string) |  | adaptive|url-test|round-robin|random (AUTO) |






<a name="aop-traffic-RoutingState"></a>

### RoutingState



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| active_node | [string](#string) |  |  |
| egress_url | [string](#string) |  |  |
| auto | [bool](#bool) |  |  |






<a name="aop-traffic-State"></a>

### State
State is the runner&#39;s reply to Configure/Query.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| routing | [RoutingState](#aop-traffic-RoutingState) |  |  |
| capture | [CaptureState](#aop-traffic-CaptureState) |  |  |
| error | [string](#string) |  |  |








<a name="aop-traffic-CaptureMode"></a>

### CaptureMode
CaptureMode selects what the hub does with traffic it routes. RELAY forwards
undecrypted and records nothing; RECORD intercepts (MITM) and stores flows.

| Name | Number | Description |
| ---- | ------ | ----------- |
| CAPTURE_MODE_UNSPECIFIED | 0 | leave capture unchanged (Configure) |
| CAPTURE_MODE_RELAY | 1 | route only: no interception, no record |
| CAPTURE_MODE_RECORD | 2 | intercept &#43; record |



<a name="aop-traffic-RoutingMode"></a>

### RoutingMode
RoutingMode selects how the egress chain is set. UNSPECIFIED leaves routing
unchanged so a Configure can steer capture without touching the proxy.

| Name | Number | Description |
| ---- | ------ | ----------- |
| ROUTING_MODE_UNSPECIFIED | 0 |  |
| ROUTING_MODE_DIRECT | 1 | revert to the original/direct egress |
| ROUTING_MODE_PROXY | 2 | single proxy URL (url) |
| ROUTING_MODE_SUBSCRIBE | 3 | load a clash subscription (url), no switch |
| ROUTING_MODE_AUTO | 4 | subscription &#43; adaptive load balancing |
| ROUTING_MODE_SWITCH | 5 | switch active node within a loaded subscription |
| ROUTING_MODE_CLEAR | 6 | clear subscription, revert to original |










## Scalar Value Types

| .proto Type | Notes | C++ | Java | Python | Go | C# | PHP | Ruby |
| ----------- | ----- | --- | ---- | ------ | -- | -- | --- | ---- |
| <a name="double" /> double |  | double | double | float | float64 | double | float | Float |
| <a name="float" /> float |  | float | float | float | float32 | float | float | Float |
| <a name="int32" /> int32 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint32 instead. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="int64" /> int64 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint64 instead. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="uint32" /> uint32 | Uses variable-length encoding. | uint32 | int | int/long | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="uint64" /> uint64 | Uses variable-length encoding. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum or Fixnum (as required) |
| <a name="sint32" /> sint32 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int32s. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sint64" /> sint64 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int64s. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="fixed32" /> fixed32 | Always four bytes. More efficient than uint32 if values are often greater than 2^28. | uint32 | int | int | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="fixed64" /> fixed64 | Always eight bytes. More efficient than uint64 if values are often greater than 2^56. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum |
| <a name="sfixed32" /> sfixed32 | Always four bytes. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sfixed64" /> sfixed64 | Always eight bytes. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="bool" /> bool |  | bool | boolean | boolean | bool | bool | boolean | TrueClass/FalseClass |
| <a name="string" /> string | A string must always contain UTF-8 encoded or 7-bit ASCII text. | string | String | str/unicode | string | string | string | String (UTF-8) |
| <a name="bytes" /> bytes | May contain any arbitrary sequence of bytes. | string | ByteString | str | []byte | ByteString | string | String (ASCII-8BIT) |
