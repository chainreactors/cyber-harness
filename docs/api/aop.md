# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [aop/chat.proto](#aop_chat-proto)
    - [CancelTurnRequest](#aop-CancelTurnRequest)
    - [CancelTurnResponse](#aop-CancelTurnResponse)
    - [CloseSessionRequest](#aop-CloseSessionRequest)
    - [CloseSessionResponse](#aop-CloseSessionResponse)
    - [EventDelivery](#aop-EventDelivery)
    - [ListEventsRequest](#aop-ListEventsRequest)
    - [ListEventsResponse](#aop-ListEventsResponse)
    - [OpenSessionRequest](#aop-OpenSessionRequest)
    - [OpenSessionResponse](#aop-OpenSessionResponse)
    - [Rejection](#aop-Rejection)
    - [RunTurnRequest](#aop-RunTurnRequest)
    - [RunTurnResponse](#aop-RunTurnResponse)
    - [Session](#aop-Session)
    - [TurnReceipt](#aop-TurnReceipt)
    - [WatchEventsRequest](#aop-WatchEventsRequest)
  
- [aop/content.proto](#aop_content-proto)
    - [Annotation](#aop-Annotation)
    - [Content](#aop-Content)
    - [MediaContent](#aop-MediaContent)
    - [Message](#aop-Message)
    - [ReasoningContent](#aop-ReasoningContent)
    - [Resource](#aop-Resource)
    - [TextContent](#aop-TextContent)
    - [ToolCall](#aop-ToolCall)
    - [ToolDefinition](#aop-ToolDefinition)
    - [ToolResult](#aop-ToolResult)
  
- [aop/envelope.proto](#aop_envelope-proto)
    - [Envelope](#aop-Envelope)
  
- [aop/event.proto](#aop_event-proto)
    - [Event](#aop-Event)
    - [MessageDelta](#aop-MessageDelta)
    - [ProtocolError](#aop-ProtocolError)
    - [ProviderFrame](#aop-ProviderFrame)
    - [ProviderMetadata](#aop-ProviderMetadata)
    - [SessionEnded](#aop-SessionEnded)
    - [SessionStarted](#aop-SessionStarted)
    - [Status](#aop-Status)
    - [TokenUsage](#aop-TokenUsage)
    - [TokenUsage.DetailEntry](#aop-TokenUsage-DetailEntry)
    - [ToolCallDelta](#aop-ToolCallDelta)
    - [TurnEnded](#aop-TurnEnded)
    - [TurnStarted](#aop-TurnStarted)
  
    - [DeltaOperation](#aop-DeltaOperation)
    - [Direction](#aop-Direction)
  
- [aop/protocol.proto](#aop_protocol-proto)
    - [AgentAccepted](#aop-AgentAccepted)
    - [AgentHello](#aop-AgentHello)
    - [AgentRuntimeInfo](#aop-AgentRuntimeInfo)
    - [AgentStats](#aop-AgentStats)
    - [AgentStatus](#aop-AgentStatus)
    - [CancelOperation](#aop-CancelOperation)
    - [ProtocolMessage](#aop-ProtocolMessage)
  
- [aop/value.proto](#aop_value-proto)
    - [EncodedValue](#aop-EncodedValue)
  
- [aop/exec/protocol.proto](#aop_exec_protocol-proto)
    - [Output](#aop-exec-Output)
    - [ProtocolMessage](#aop-exec-ProtocolMessage)
    - [Request](#aop-exec-Request)
    - [Request.EnvEntry](#aop-exec-Request-EnvEntry)
    - [Result](#aop-exec-Result)
  
    - [Stream](#aop-exec-Stream)
  
- [aop/file/protocol.proto](#aop_file_protocol-proto)
    - [Entry](#aop-file-Entry)
    - [ListRequest](#aop-file-ListRequest)
    - [MkdirRequest](#aop-file-MkdirRequest)
    - [ProtocolMessage](#aop-file-ProtocolMessage)
    - [ReadRequest](#aop-file-ReadRequest)
    - [Result](#aop-file-Result)
    - [UploadRequest](#aop-file-UploadRequest)
    - [WriteRequest](#aop-file-WriteRequest)
  
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
    - [Call](#aop-tool-Call)
    - [Progress](#aop-tool-Progress)
    - [ProtocolMessage](#aop-tool-ProtocolMessage)
  
- [Scalar Value Types](#scalar-value-types)



<a name="aop_chat-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/chat.proto



<a name="aop-CancelTurnRequest"></a>

### CancelTurnRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| turn_id | [string](#string) |  |  |
| reason | [string](#string) |  |  |






<a name="aop-CancelTurnResponse"></a>

### CancelTurnResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| accepted | [TurnReceipt](#aop-TurnReceipt) |  |  |
| rejected | [Rejection](#aop-Rejection) |  |  |






<a name="aop-CloseSessionRequest"></a>

### CloseSessionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| reason | [string](#string) |  |  |






<a name="aop-CloseSessionResponse"></a>

### CloseSessionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| accepted | [Session](#aop-Session) |  |  |
| rejected | [Rejection](#aop-Rejection) |  |  |






<a name="aop-EventDelivery"></a>

### EventDelivery



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| cursor | [string](#string) |  |  |
| event | [Event](#aop-Event) |  |  |






<a name="aop-ListEventsRequest"></a>

### ListEventsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| after_cursor | [string](#string) |  |  |
| limit | [uint32](#uint32) |  |  |






<a name="aop-ListEventsResponse"></a>

### ListEventsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| events | [EventDelivery](#aop-EventDelivery) | repeated |  |
| next_cursor | [string](#string) |  |  |






<a name="aop-OpenSessionRequest"></a>

### OpenSessionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| node_id | [string](#string) |  |  |
| title | [string](#string) |  |  |
| parent_session_id | [string](#string) |  |  |
| parent_tool_call_id | [string](#string) |  |  |
| extensions | [google.protobuf.Any](#google-protobuf-Any) | repeated |  |






<a name="aop-OpenSessionResponse"></a>

### OpenSessionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| accepted | [Session](#aop-Session) |  |  |
| rejected | [Rejection](#aop-Rejection) |  |  |






<a name="aop-Rejection"></a>

### Rejection



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| code | [string](#string) |  |  |
| message | [string](#string) |  |  |
| retryable | [bool](#bool) |  |  |






<a name="aop-RunTurnRequest"></a>

### RunTurnRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| turn_id | [string](#string) |  |  |
| input | [Message](#aop-Message) |  |  |
| continue_session | [bool](#bool) |  |  |
| max_turns | [uint32](#uint32) |  |  |
| extensions | [google.protobuf.Any](#google-protobuf-Any) | repeated |  |






<a name="aop-RunTurnResponse"></a>

### RunTurnResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| accepted | [TurnReceipt](#aop-TurnReceipt) |  |  |
| rejected | [Rejection](#aop-Rejection) |  |  |






<a name="aop-Session"></a>

### Session



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| state | [string](#string) |  |  |
| node_id | [string](#string) |  |  |
| title | [string](#string) |  |  |






<a name="aop-TurnReceipt"></a>

### TurnReceipt



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| turn_id | [string](#string) |  |  |
| state | [string](#string) |  |  |






<a name="aop-WatchEventsRequest"></a>

### WatchEventsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| after_cursor | [string](#string) |  |  |





 

 

 

 



<a name="aop_content-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/content.proto



<a name="aop-Annotation"></a>

### Annotation



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| type | [string](#string) |  |  |
| start | [uint64](#uint64) |  |  |
| end | [uint64](#uint64) |  |  |
| title | [string](#string) |  |  |
| uri | [string](#string) |  |  |






<a name="aop-Content"></a>

### Content



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| text | [TextContent](#aop-TextContent) |  |  |
| reasoning | [ReasoningContent](#aop-ReasoningContent) |  |  |
| refusal | [string](#string) |  |  |
| media | [MediaContent](#aop-MediaContent) |  |  |
| tool_call | [ToolCall](#aop-ToolCall) |  |  |
| tool_result | [ToolResult](#aop-ToolResult) |  |  |






<a name="aop-MediaContent"></a>

### MediaContent



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| kind | [string](#string) |  |  |
| resource | [Resource](#aop-Resource) |  |  |
| transcript | [string](#string) |  |  |






<a name="aop-Message"></a>

### Message



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| role | [string](#string) |  |  |
| name | [string](#string) |  |  |
| content | [Content](#aop-Content) | repeated |  |






<a name="aop-ReasoningContent"></a>

### ReasoningContent



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| text | [string](#string) |  |  |






<a name="aop-Resource"></a>

### Resource



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [bytes](#bytes) |  |  |
| uri | [string](#string) |  |  |
| media_type | [string](#string) |  |  |
| filename | [string](#string) |  |  |






<a name="aop-TextContent"></a>

### TextContent



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| text | [string](#string) |  |  |
| annotations | [Annotation](#aop-Annotation) | repeated |  |






<a name="aop-ToolCall"></a>

### ToolCall



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| name | [string](#string) |  |  |
| kind | [string](#string) |  |  |
| arguments | [EncodedValue](#aop-EncodedValue) |  |  |
| working_directory | [string](#string) |  |  |






<a name="aop-ToolDefinition"></a>

### ToolDefinition
ToolDefinition is the provider-neutral function/tool contract advertised by
an Agent. Provider adapters translate this schema at their wire boundary.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| type | [string](#string) |  |  |
| name | [string](#string) |  |  |
| description | [string](#string) |  |  |
| input_schema | [EncodedValue](#aop-EncodedValue) |  |  |






<a name="aop-ToolResult"></a>

### ToolResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| call_id | [string](#string) |  |  |
| output | [Content](#aop-Content) | repeated |  |
| is_error | [bool](#bool) |  |  |
| name | [string](#string) |  |  |
| duration_ms | [uint64](#uint64) |  |  |
| terminate | [bool](#bool) |  |  |





 

 

 

 



<a name="aop_envelope-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/envelope.proto



<a name="aop-Envelope"></a>

### Envelope
Envelope is the only AOP wire envelope. Business namespaces are carried by
Any and never extend this message with a global oneof.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| reply_to | [string](#string) |  |  |
| delivery_cursor | [string](#string) |  |  |
| payload | [google.protobuf.Any](#google-protobuf-Any) |  |  |





 

 

 

 



<a name="aop_event-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/event.proto



<a name="aop-Event"></a>

### Event



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| emitted_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| session_id | [string](#string) |  |  |
| turn_id | [string](#string) |  |  |
| emitter | [string](#string) |  |  |
| seq | [uint64](#uint64) |  |  |
| extensions | [google.protobuf.Any](#google-protobuf-Any) | repeated |  |
| session_started | [SessionStarted](#aop-SessionStarted) |  |  |
| session_ended | [SessionEnded](#aop-SessionEnded) |  |  |
| turn_started | [TurnStarted](#aop-TurnStarted) |  |  |
| turn_ended | [TurnEnded](#aop-TurnEnded) |  |  |
| message | [Message](#aop-Message) |  |  |
| message_delta | [MessageDelta](#aop-MessageDelta) |  |  |
| tool_call | [ToolCall](#aop-ToolCall) |  |  |
| tool_call_delta | [ToolCallDelta](#aop-ToolCallDelta) |  |  |
| tool_result | [ToolResult](#aop-ToolResult) |  |  |
| usage | [TokenUsage](#aop-TokenUsage) |  |  |
| error | [ProtocolError](#aop-ProtocolError) |  |  |
| status | [Status](#aop-Status) |  |  |
| provider_frame | [ProviderFrame](#aop-ProviderFrame) |  |  |
| extension | [google.protobuf.Any](#google-protobuf-Any) |  |  |






<a name="aop-MessageDelta"></a>

### MessageDelta



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| message_id | [string](#string) |  |  |
| content_index | [uint32](#uint32) |  |  |
| operation | [DeltaOperation](#aop-DeltaOperation) |  |  |
| text | [string](#string) |  |  |
| reasoning | [string](#string) |  |  |
| refusal | [string](#string) |  |  |
| data | [bytes](#bytes) |  |  |
| tool_arguments | [string](#string) |  |  |
| content | [Content](#aop-Content) |  |  |






<a name="aop-ProtocolError"></a>

### ProtocolError



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| code | [string](#string) |  |  |
| message | [string](#string) |  |  |
| retryable | [bool](#bool) |  |  |






<a name="aop-ProviderFrame"></a>

### ProviderFrame
ProviderFrame preserves one exact provider body or stream frame.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| provider | [string](#string) |  |  |
| protocol | [string](#string) |  |  |
| event_type | [string](#string) |  |  |
| direction | [Direction](#aop-Direction) |  |  |
| transport | [string](#string) |  |  |
| payload | [bytes](#bytes) |  |  |
| media_type | [string](#string) |  |  |
| metadata | [ProviderMetadata](#aop-ProviderMetadata) | repeated |  |






<a name="aop-ProviderMetadata"></a>

### ProviderMetadata



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| value | [bytes](#bytes) |  |  |






<a name="aop-SessionEnded"></a>

### SessionEnded



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reason | [string](#string) |  |  |






<a name="aop-SessionStarted"></a>

### SessionStarted



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| model | [string](#string) |  |  |
| parent_session_id | [string](#string) |  |  |
| parent_tool_call_id | [string](#string) |  |  |






<a name="aop-Status"></a>

### Status



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| state | [string](#string) |  |  |






<a name="aop-TokenUsage"></a>

### TokenUsage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| input_tokens | [uint64](#uint64) |  |  |
| output_tokens | [uint64](#uint64) |  |  |
| total_tokens | [uint64](#uint64) |  |  |
| model | [string](#string) |  |  |
| detail | [TokenUsage.DetailEntry](#aop-TokenUsage-DetailEntry) | repeated |  |






<a name="aop-TokenUsage-DetailEntry"></a>

### TokenUsage.DetailEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [uint64](#uint64) |  |  |






<a name="aop-ToolCallDelta"></a>

### ToolCallDelta



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| call_id | [string](#string) |  |  |
| index | [uint32](#uint32) |  |  |
| name | [string](#string) |  |  |
| arguments | [bytes](#bytes) |  |  |






<a name="aop-TurnEnded"></a>

### TurnEnded



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stop_reason | [string](#string) |  |  |
| error | [ProtocolError](#aop-ProtocolError) |  |  |
| usage | [TokenUsage](#aop-TokenUsage) |  |  |
| context_tokens | [uint64](#uint64) |  |  |






<a name="aop-TurnStarted"></a>

### TurnStarted






 


<a name="aop-DeltaOperation"></a>

### DeltaOperation


| Name | Number | Description |
| ---- | ------ | ----------- |
| DELTA_OPERATION_UNSPECIFIED | 0 |  |
| DELTA_OPERATION_START | 1 |  |
| DELTA_OPERATION_APPEND | 2 |  |
| DELTA_OPERATION_REPLACE | 3 |  |
| DELTA_OPERATION_END | 4 |  |



<a name="aop-Direction"></a>

### Direction


| Name | Number | Description |
| ---- | ------ | ----------- |
| DIRECTION_UNSPECIFIED | 0 |  |
| DIRECTION_REQUEST | 1 |  |
| DIRECTION_RESPONSE | 2 |  |


 

 

 



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





 

 

 

 



<a name="aop_value-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## aop/value.proto



<a name="aop-EncodedValue"></a>

### EncodedValue
EncodedValue carries genuinely opaque data whose schema is not protobuf,
notably provider/tool JSON arguments and JSON Schema documents.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [bytes](#bytes) |  |  |
| media_type | [string](#string) |  |  |





 

 

 

 



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



<a name="aop-tool-Call"></a>

### Call



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| turn_id | [string](#string) |  |  |
| call | [aop.ToolCall](#aop-ToolCall) |  |  |






<a name="aop-tool-Progress"></a>

### Progress



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tool | [string](#string) |  |  |
| target | [string](#string) |  |  |
| timestamp | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| text | [string](#string) |  |  |






<a name="aop-tool-ProtocolMessage"></a>

### ProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| progress | [Progress](#aop-tool-Progress) |  |  |
| call | [Call](#aop-tool-Call) |  |  |





 

 

 

 



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

