# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [rpc/agent.proto](#rpc_agent-proto)
    - [AgentService](#cyber-rpc-agent-AgentService)

- [rpc/aop.proto](#rpc_aop-proto)
    - [AOPService](#cyber-rpc-aop-AOPService)

- [rpc/chat.proto](#rpc_chat-proto)
    - [SessionService](#cyber-rpc-chat-SessionService)

- [rpc/config.proto](#rpc_config-proto)
    - [ConfigService](#cyber-rpc-config-ConfigService)

- [rpc/scan.proto](#rpc_scan-proto)
    - [ScanService](#cyber-rpc-scan-ScanService)

- [rpc/sco.proto](#rpc_sco-proto)
    - [SCOService](#cyber-rpc-sco-SCOService)

- [rpc/system.proto](#rpc_system-proto)
    - [SystemService](#cyber-rpc-system-SystemService)

- [types/agent.proto](#types_agent-proto)
    - [AgentListEntry](#cyber-agent-AgentListEntry)
    - [AgentListMetadata](#cyber-agent-AgentListMetadata)
    - [AgentRunOptions](#cyber-agent-AgentRunOptions)
    - [AgentView](#cyber-agent-AgentView)
    - [BudgetWarning](#cyber-agent-BudgetWarning)
    - [CommandDetail](#cyber-agent-CommandDetail)
    - [CompactDetail](#cyber-agent-CompactDetail)
    - [DelegationDetail](#cyber-agent-DelegationDetail)
    - [EvalControl](#cyber-agent-EvalControl)
    - [EvalDetail](#cyber-agent-EvalDetail)
    - [LLMRequestDetail](#cyber-agent-LLMRequestDetail)
    - [ListAgentsRequest](#cyber-agent-ListAgentsRequest)
    - [ListAgentsResponse](#cyber-agent-ListAgentsResponse)
    - [WebMessageMetadata](#cyber-agent-WebMessageMetadata)

- [types/chat.proto](#types_chat-proto)
    - [DeleteSessionRequest](#cyber-chat-DeleteSessionRequest)
    - [DeleteSessionResponse](#cyber-chat-DeleteSessionResponse)
    - [GetSessionRequest](#cyber-chat-GetSessionRequest)
    - [GetSessionResponse](#cyber-chat-GetSessionResponse)
    - [ListCommandsRequest](#cyber-chat-ListCommandsRequest)
    - [ListCommandsResponse](#cyber-chat-ListCommandsResponse)
    - [ListSessionsRequest](#cyber-chat-ListSessionsRequest)
    - [ListSessionsResponse](#cyber-chat-ListSessionsResponse)
    - [ResetSessionReceipt](#cyber-chat-ResetSessionReceipt)
    - [ResetSessionRequest](#cyber-chat-ResetSessionRequest)
    - [ResetSessionResponse](#cyber-chat-ResetSessionResponse)
    - [SessionHistory](#cyber-chat-SessionHistory)
    - [SessionRecord](#cyber-chat-SessionRecord)

    - [SessionHistory.Mode](#cyber-chat-SessionHistory-Mode)

- [types/command.proto](#types_command-proto)
    - [CommandCatalog](#cyber-command-CommandCatalog)
    - [CommandProtocolMessage](#cyber-command-CommandProtocolMessage)
    - [CommandReceipt](#cyber-command-CommandReceipt)
    - [CommandRequest](#cyber-command-CommandRequest)
    - [CommandResult](#cyber-command-CommandResult)
    - [CommandSpec](#cyber-command-CommandSpec)

- [types/config.proto](#types_config-proto)
    - [ActivateProfileRequest](#cyber-config-ActivateProfileRequest)
    - [ActivateProfileResponse](#cyber-config-ActivateProfileResponse)
    - [AgentConfig](#cyber-config-AgentConfig)
    - [ConfigView](#cyber-config-ConfigView)
    - [ConnectionCheck](#cyber-config-ConnectionCheck)
    - [CyberhubConfig](#cyber-config-CyberhubConfig)
    - [CyberhubView](#cyber-config-CyberhubView)
    - [DistributeConfig](#cyber-config-DistributeConfig)
    - [GetConfigRequest](#cyber-config-GetConfigRequest)
    - [GetConfigResponse](#cyber-config-GetConfigResponse)
    - [IOAConfig](#cyber-config-IOAConfig)
    - [IOAView](#cyber-config-IOAView)
    - [LLMConfig](#cyber-config-LLMConfig)
    - [LLMProbeRequest](#cyber-config-LLMProbeRequest)
    - [LLMProbeResult](#cyber-config-LLMProbeResult)
    - [LLMProviderConfig](#cyber-config-LLMProviderConfig)
    - [LLMProviderView](#cyber-config-LLMProviderView)
    - [LLMView](#cyber-config-LLMView)
    - [ListModelsResult](#cyber-config-ListModelsResult)
    - [ReconConfig](#cyber-config-ReconConfig)
    - [ReconView](#cyber-config-ReconView)
    - [ScanConfig](#cyber-config-ScanConfig)
    - [SearchConfig](#cyber-config-SearchConfig)
    - [SearchView](#cyber-config-SearchView)
    - [TestConnectionRequest](#cyber-config-TestConnectionRequest)
    - [TestConnectionResponse](#cyber-config-TestConnectionResponse)
    - [UpdateConfigRequest](#cyber-config-UpdateConfigRequest)
    - [UpdateConfigResponse](#cyber-config-UpdateConfigResponse)

- [types/reload.proto](#types_reload-proto)
    - [ReloadProtocolMessage](#cyber-reload-ReloadProtocolMessage)
    - [ReloadRequest](#cyber-reload-ReloadRequest)
    - [ReloadResult](#cyber-reload-ReloadResult)

- [types/scan.proto](#types_scan-proto)
    - [CancelScanRequest](#cyber-scan-CancelScanRequest)
    - [CancelScanResponse](#cyber-scan-CancelScanResponse)
    - [GetScanReportRequest](#cyber-scan-GetScanReportRequest)
    - [GetScanReportResponse](#cyber-scan-GetScanReportResponse)
    - [GetScanRequest](#cyber-scan-GetScanRequest)
    - [GetScanResponse](#cyber-scan-GetScanResponse)
    - [ListScansRequest](#cyber-scan-ListScansRequest)
    - [ListScansResponse](#cyber-scan-ListScansResponse)
    - [Scan](#cyber-scan-Scan)
    - [ScanCompleted](#cyber-scan-ScanCompleted)
    - [ScanEvent](#cyber-scan-ScanEvent)
    - [ScanFailed](#cyber-scan-ScanFailed)
    - [ScanOptions](#cyber-scan-ScanOptions)
    - [ScanProgress](#cyber-scan-ScanProgress)
    - [ScanProtocolMessage](#cyber-scan-ScanProtocolMessage)
    - [SessionBinding](#cyber-scan-SessionBinding)
    - [SessionScanEvent](#cyber-scan-SessionScanEvent)
    - [SubmitScanRequest](#cyber-scan-SubmitScanRequest)
    - [SubmitScanResponse](#cyber-scan-SubmitScanResponse)
    - [WatchScanEventsRequest](#cyber-scan-WatchScanEventsRequest)

    - [ScanStatus](#cyber-scan-ScanStatus)

- [types/sco.proto](#types_sco-proto)
    - [DeleteNodesRequest](#cyber-sco-DeleteNodesRequest)
    - [DeleteNodesResponse](#cyber-sco-DeleteNodesResponse)
    - [GetNodeRequest](#cyber-sco-GetNodeRequest)
    - [GetNodeResponse](#cyber-sco-GetNodeResponse)
    - [GetStatsRequest](#cyber-sco-GetStatsRequest)
    - [GetStatsResponse](#cyber-sco-GetStatsResponse)
    - [GetStatsResponse.ValuesEntry](#cyber-sco-GetStatsResponse-ValuesEntry)
    - [ImportNodesRequest](#cyber-sco-ImportNodesRequest)
    - [ImportNodesResponse](#cyber-sco-ImportNodesResponse)
    - [ListArtifactsRequest](#cyber-sco-ListArtifactsRequest)
    - [ListArtifactsResponse](#cyber-sco-ListArtifactsResponse)
    - [ListNodesRequest](#cyber-sco-ListNodesRequest)
    - [ListNodesResponse](#cyber-sco-ListNodesResponse)

- [types/system.proto](#types_system-proto)
    - [GetStatusRequest](#cyber-system-GetStatusRequest)
    - [GetStatusResponse](#cyber-system-GetStatusResponse)
    - [SystemStatus](#cyber-system-SystemStatus)

- [Scalar Value Types](#scalar-value-types)



<a name="rpc_agent-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/agent.proto









<a name="cyber-rpc-agent-AgentService"></a>

### AgentService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| ListAgents | [.cyber.agent.ListAgentsRequest](#cyber-agent-ListAgentsRequest) | [.cyber.agent.ListAgentsResponse](#cyber-agent-ListAgentsResponse) |  |





<a name="rpc_aop-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/aop.proto









<a name="cyber-rpc-aop-AOPService"></a>

### AOPService
AOPService exposes the application protocol as one bidirectional Envelope
stream. Native clients may use Connect or gRPC; browser clients keep using
the WebSocket compatibility transport over the same service core.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| Connect | [.aop.Envelope](#aop-Envelope) stream | [.aop.Envelope](#aop-Envelope) stream |  |





<a name="rpc_chat-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/chat.proto









<a name="cyber-rpc-chat-SessionService"></a>

### SessionService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| ListSessions | [.cyber.chat.ListSessionsRequest](#cyber-chat-ListSessionsRequest) | [.cyber.chat.ListSessionsResponse](#cyber-chat-ListSessionsResponse) |  |
| GetSession | [.cyber.chat.GetSessionRequest](#cyber-chat-GetSessionRequest) | [.cyber.chat.GetSessionResponse](#cyber-chat-GetSessionResponse) |  |
| ResetSession | [.cyber.chat.ResetSessionRequest](#cyber-chat-ResetSessionRequest) | [.cyber.chat.ResetSessionResponse](#cyber-chat-ResetSessionResponse) |  |
| DeleteSession | [.cyber.chat.DeleteSessionRequest](#cyber-chat-DeleteSessionRequest) | [.cyber.chat.DeleteSessionResponse](#cyber-chat-DeleteSessionResponse) |  |
| ListCommands | [.cyber.chat.ListCommandsRequest](#cyber-chat-ListCommandsRequest) | [.cyber.chat.ListCommandsResponse](#cyber-chat-ListCommandsResponse) |  |
| ListEvents | [.aop.ListEventsRequest](#aop-ListEventsRequest) | [.aop.ListEventsResponse](#aop-ListEventsResponse) |  |





<a name="rpc_config-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/config.proto









<a name="cyber-rpc-config-ConfigService"></a>

### ConfigService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| GetConfig | [.cyber.config.GetConfigRequest](#cyber-config-GetConfigRequest) | [.cyber.config.GetConfigResponse](#cyber-config-GetConfigResponse) |  |
| UpdateConfig | [.cyber.config.UpdateConfigRequest](#cyber-config-UpdateConfigRequest) | [.cyber.config.UpdateConfigResponse](#cyber-config-UpdateConfigResponse) |  |
| ActivateProfile | [.cyber.config.ActivateProfileRequest](#cyber-config-ActivateProfileRequest) | [.cyber.config.ActivateProfileResponse](#cyber-config-ActivateProfileResponse) |  |
| TestLLM | [.cyber.config.LLMProbeRequest](#cyber-config-LLMProbeRequest) | [.cyber.config.LLMProbeResult](#cyber-config-LLMProbeResult) |  |
| ListModels | [.cyber.config.LLMProbeRequest](#cyber-config-LLMProbeRequest) | [.cyber.config.ListModelsResult](#cyber-config-ListModelsResult) |  |
| TestConnection | [.cyber.config.TestConnectionRequest](#cyber-config-TestConnectionRequest) | [.cyber.config.TestConnectionResponse](#cyber-config-TestConnectionResponse) |  |





<a name="rpc_scan-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/scan.proto









<a name="cyber-rpc-scan-ScanService"></a>

### ScanService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| SubmitScan | [.cyber.scan.SubmitScanRequest](#cyber-scan-SubmitScanRequest) | [.cyber.scan.SubmitScanResponse](#cyber-scan-SubmitScanResponse) |  |
| GetScan | [.cyber.scan.GetScanRequest](#cyber-scan-GetScanRequest) | [.cyber.scan.GetScanResponse](#cyber-scan-GetScanResponse) |  |
| ListScans | [.cyber.scan.ListScansRequest](#cyber-scan-ListScansRequest) | [.cyber.scan.ListScansResponse](#cyber-scan-ListScansResponse) |  |
| CancelScan | [.cyber.scan.CancelScanRequest](#cyber-scan-CancelScanRequest) | [.cyber.scan.CancelScanResponse](#cyber-scan-CancelScanResponse) |  |
| GetScanReport | [.cyber.scan.GetScanReportRequest](#cyber-scan-GetScanReportRequest) | [.cyber.scan.GetScanReportResponse](#cyber-scan-GetScanReportResponse) |  |





<a name="rpc_sco-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/sco.proto









<a name="cyber-rpc-sco-SCOService"></a>

### SCOService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| ListNodes | [.cyber.sco.ListNodesRequest](#cyber-sco-ListNodesRequest) | [.cyber.sco.ListNodesResponse](#cyber-sco-ListNodesResponse) |  |
| GetNode | [.cyber.sco.GetNodeRequest](#cyber-sco-GetNodeRequest) | [.cyber.sco.GetNodeResponse](#cyber-sco-GetNodeResponse) |  |
| GetStats | [.cyber.sco.GetStatsRequest](#cyber-sco-GetStatsRequest) | [.cyber.sco.GetStatsResponse](#cyber-sco-GetStatsResponse) |  |
| DeleteNodes | [.cyber.sco.DeleteNodesRequest](#cyber-sco-DeleteNodesRequest) | [.cyber.sco.DeleteNodesResponse](#cyber-sco-DeleteNodesResponse) |  |
| ImportNodes | [.cyber.sco.ImportNodesRequest](#cyber-sco-ImportNodesRequest) | [.cyber.sco.ImportNodesResponse](#cyber-sco-ImportNodesResponse) |  |
| ListArtifacts | [.cyber.sco.ListArtifactsRequest](#cyber-sco-ListArtifactsRequest) | [.cyber.sco.ListArtifactsResponse](#cyber-sco-ListArtifactsResponse) |  |





<a name="rpc_system-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/system.proto









<a name="cyber-rpc-system-SystemService"></a>

### SystemService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| GetStatus | [.cyber.system.GetStatusRequest](#cyber-system-GetStatusRequest) | [.cyber.system.GetStatusResponse](#cyber-system-GetStatusResponse) |  |





<a name="types_agent-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/agent.proto



<a name="cyber-agent-AgentListEntry"></a>

### AgentListEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| node_id | [string](#string) |  |  |
| busy | [bool](#bool) |  |  |
| provider | [string](#string) |  |  |
| model | [string](#string) |  |  |






<a name="cyber-agent-AgentListMetadata"></a>

### AgentListMetadata



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| agents | [AgentListEntry](#cyber-agent-AgentListEntry) | repeated |  |






<a name="cyber-agent-AgentRunOptions"></a>

### AgentRunOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| eval_criteria | [string](#string) |  |  |
| eval_max_rounds | [uint32](#uint32) |  |  |






<a name="cyber-agent-AgentView"></a>

### AgentView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| hello | [aop.AgentHello](#aop-AgentHello) |  |  |
| status | [aop.AgentStatus](#aop-AgentStatus) |  |  |
| stats | [aop.AgentStats](#aop-AgentStats) |  |  |
| connected_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| commands | [cyber.command.CommandSpec](#cyber-command-CommandSpec) | repeated |  |
| busy | [bool](#bool) |  |  |






<a name="cyber-agent-BudgetWarning"></a>

### BudgetWarning



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| context_tokens | [uint64](#uint64) |  |  |
| token_budget | [uint64](#uint64) |  |  |






<a name="cyber-agent-CommandDetail"></a>

### CommandDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| line | [string](#string) |  |  |
| presentation | [string](#string) |  |  |






<a name="cyber-agent-CompactDetail"></a>

### CompactDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| error | [string](#string) |  |  |
| kept_messages | [uint64](#uint64) |  |  |
| tokens_after | [uint64](#uint64) |  |  |
| tokens_before | [uint64](#uint64) |  |  |






<a name="cyber-agent-DelegationDetail"></a>

### DelegationDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| agent_id | [string](#string) |  |  |
| agent_name | [string](#string) |  |  |
| agent_type | [string](#string) |  |  |
| context_mode | [string](#string) |  |  |
| run_mode | [string](#string) |  |  |
| task | [string](#string) |  |  |






<a name="cyber-agent-EvalControl"></a>

### EvalControl



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| criteria | [string](#string) |  |  |
| max_rounds | [uint32](#uint32) |  |  |






<a name="cyber-agent-EvalDetail"></a>

### EvalDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| error | [string](#string) |  |  |
| max_rounds | [uint32](#uint32) |  |  |
| pass | [bool](#bool) |  |  |
| reason | [string](#string) |  |  |
| round | [uint32](#uint32) |  |  |






<a name="cyber-agent-LLMRequestDetail"></a>

### LLMRequestDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| model | [string](#string) |  |  |
| messages | [uint32](#uint32) |  |  |
| max_tokens | [uint32](#uint32) |  |  |
| stream | [bool](#bool) |  |  |






<a name="cyber-agent-ListAgentsRequest"></a>

### ListAgentsRequest







<a name="cyber-agent-ListAgentsResponse"></a>

### ListAgentsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| agents | [AgentView](#cyber-agent-AgentView) | repeated |  |






<a name="cyber-agent-WebMessageMetadata"></a>

### WebMessageMetadata



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| node_id | [string](#string) |  |  |
| code | [string](#string) |  |  |
| params | [google.protobuf.Struct](#google-protobuf-Struct) |  |  |
| agent_list | [AgentListMetadata](#cyber-agent-AgentListMetadata) |  |  |















<a name="types_chat-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/chat.proto



<a name="cyber-chat-DeleteSessionRequest"></a>

### DeleteSessionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| session_id | [string](#string) |  |  |






<a name="cyber-chat-DeleteSessionResponse"></a>

### DeleteSessionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| accepted | [aop.Session](#aop-Session) |  |  |
| rejected | [aop.Rejection](#aop-Rejection) |  |  |






<a name="cyber-chat-GetSessionRequest"></a>

### GetSessionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |






<a name="cyber-chat-GetSessionResponse"></a>

### GetSessionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session | [SessionRecord](#cyber-chat-SessionRecord) |  |  |






<a name="cyber-chat-ListCommandsRequest"></a>

### ListCommandsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |






<a name="cyber-chat-ListCommandsResponse"></a>

### ListCommandsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| commands | [cyber.command.CommandSpec](#cyber-command-CommandSpec) | repeated |  |






<a name="cyber-chat-ListSessionsRequest"></a>

### ListSessionsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| after_cursor | [string](#string) |  |  |
| limit | [uint32](#uint32) |  |  |
| include_closed | [bool](#bool) |  |  |






<a name="cyber-chat-ListSessionsResponse"></a>

### ListSessionsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sessions | [SessionRecord](#cyber-chat-SessionRecord) | repeated |  |
| next_cursor | [string](#string) |  |  |






<a name="cyber-chat-ResetSessionReceipt"></a>

### ResetSessionReceipt



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| previous | [aop.Session](#aop-Session) |  |  |
| current | [SessionRecord](#cyber-chat-SessionRecord) |  |  |






<a name="cyber-chat-ResetSessionRequest"></a>

### ResetSessionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| session_id | [string](#string) |  |  |
| new_session_id | [string](#string) |  |  |
| title | [string](#string) |  |  |






<a name="cyber-chat-ResetSessionResponse"></a>

### ResetSessionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| accepted | [ResetSessionReceipt](#cyber-chat-ResetSessionReceipt) |  |  |
| rejected | [aop.Rejection](#aop-Rejection) |  |  |






<a name="cyber-chat-SessionHistory"></a>

### SessionHistory
SessionHistory is persisted as an AOP event extension. It makes transcript
inheritance explicit without changing the shared AOP protocol schema.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| mode | [SessionHistory.Mode](#cyber-chat-SessionHistory-Mode) |  |  |






<a name="cyber-chat-SessionRecord"></a>

### SessionRecord



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session | [aop.Session](#aop-Session) |  |  |
| agent_name | [string](#string) |  |  |
| scan_ids | [string](#string) | repeated |  |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |








<a name="cyber-chat-SessionHistory-Mode"></a>

### SessionHistory.Mode


| Name | Number | Description |
| ---- | ------ | ----------- |
| MODE_UNSPECIFIED | 0 |  |
| MODE_INHERIT | 1 |  |
| MODE_SNAPSHOT | 2 |  |










<a name="types_command-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/command.proto



<a name="cyber-command-CommandCatalog"></a>

### CommandCatalog



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| commands | [CommandSpec](#cyber-command-CommandSpec) | repeated |  |






<a name="cyber-command-CommandProtocolMessage"></a>

### CommandProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request | [CommandRequest](#cyber-command-CommandRequest) |  |  |
| result | [CommandResult](#cyber-command-CommandResult) |  |  |
| catalog | [CommandCatalog](#cyber-command-CommandCatalog) |  |  |
| receipt | [CommandReceipt](#cyber-command-CommandReceipt) |  |  |






<a name="cyber-command-CommandReceipt"></a>

### CommandReceipt



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| operation_id | [string](#string) |  |  |
| session_id | [string](#string) |  |  |
| state | [string](#string) |  |  |






<a name="cyber-command-CommandRequest"></a>

### CommandRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| line | [string](#string) |  |  |






<a name="cyber-command-CommandResult"></a>

### CommandResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| command | [string](#string) |  |  |
| presentation | [string](#string) |  |  |
| content | [aop.Content](#aop-Content) | repeated |  |






<a name="cyber-command-CommandSpec"></a>

### CommandSpec



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| aliases | [string](#string) | repeated |  |
| usage | [string](#string) |  |  |
| description | [string](#string) |  |  |















<a name="types_config-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/config.proto



<a name="cyber-config-ActivateProfileRequest"></a>

### ActivateProfileRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| profile_id | [string](#string) |  |  |






<a name="cyber-config-ActivateProfileResponse"></a>

### ActivateProfileResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [ConfigView](#cyber-config-ConfigView) |  |  |






<a name="cyber-config-AgentConfig"></a>

### AgentConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tools | [string](#string) | repeated |  |
| timeout | [int32](#int32) |  |  |






<a name="cyber-config-ConfigView"></a>

### ConfigView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| path | [string](#string) |  |  |
| loaded | [bool](#bool) |  |  |
| llm | [LLMView](#cyber-config-LLMView) |  |  |
| cyberhub | [CyberhubView](#cyber-config-CyberhubView) |  |  |
| recon | [ReconView](#cyber-config-ReconView) |  |  |
| scan | [ScanConfig](#cyber-config-ScanConfig) |  |  |
| search | [SearchView](#cyber-config-SearchView) |  |  |
| ioa | [IOAView](#cyber-config-IOAView) |  |  |
| agent | [AgentConfig](#cyber-config-AgentConfig) |  |  |






<a name="cyber-config-ConnectionCheck"></a>

### ConnectionCheck



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| ok | [bool](#bool) |  |  |
| latency_ms | [int64](#int64) |  |  |
| detail | [string](#string) |  |  |
| error | [string](#string) |  |  |






<a name="cyber-config-CyberhubConfig"></a>

### CyberhubConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| url | [string](#string) |  |  |
| key | [string](#string) |  |  |
| mode | [string](#string) |  |  |
| proxy | [string](#string) |  |  |






<a name="cyber-config-CyberhubView"></a>

### CyberhubView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| url | [string](#string) |  |  |
| key_configured | [bool](#bool) |  |  |
| mode | [string](#string) |  |  |
| proxy | [string](#string) |  |  |






<a name="cyber-config-DistributeConfig"></a>

### DistributeConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| llm | [LLMConfig](#cyber-config-LLMConfig) |  |  |
| cyberhub | [CyberhubConfig](#cyber-config-CyberhubConfig) |  |  |
| recon | [ReconConfig](#cyber-config-ReconConfig) |  |  |
| scan | [ScanConfig](#cyber-config-ScanConfig) |  |  |
| search | [SearchConfig](#cyber-config-SearchConfig) |  |  |
| ioa | [IOAConfig](#cyber-config-IOAConfig) |  |  |
| agent | [AgentConfig](#cyber-config-AgentConfig) |  |  |






<a name="cyber-config-GetConfigRequest"></a>

### GetConfigRequest







<a name="cyber-config-GetConfigResponse"></a>

### GetConfigResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [ConfigView](#cyber-config-ConfigView) |  |  |






<a name="cyber-config-IOAConfig"></a>

### IOAConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| url | [string](#string) |  |  |
| token | [string](#string) |  |  |
| node_name | [string](#string) |  |  |
| space | [string](#string) |  |  |






<a name="cyber-config-IOAView"></a>

### IOAView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| url | [string](#string) |  |  |
| token_configured | [bool](#bool) |  |  |
| node_name | [string](#string) |  |  |
| space | [string](#string) |  |  |






<a name="cyber-config-LLMConfig"></a>

### LLMConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| active_profile | [string](#string) |  |  |
| providers | [LLMProviderConfig](#cyber-config-LLMProviderConfig) | repeated |  |






<a name="cyber-config-LLMProbeRequest"></a>

### LLMProbeRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| profile_id | [string](#string) |  |  |
| provider | [string](#string) |  |  |
| base_url | [string](#string) |  |  |
| api_key | [string](#string) |  |  |
| model | [string](#string) |  |  |
| proxy | [string](#string) |  |  |






<a name="cyber-config-LLMProbeResult"></a>

### LLMProbeResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ok | [bool](#bool) |  |  |
| provider | [string](#string) |  |  |
| model | [string](#string) |  |  |
| latency_ms | [int64](#int64) |  |  |
| reply | [string](#string) |  |  |
| error | [string](#string) |  |  |






<a name="cyber-config-LLMProviderConfig"></a>

### LLMProviderConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| name | [string](#string) |  |  |
| provider | [string](#string) |  |  |
| base_url | [string](#string) |  |  |
| api_key | [string](#string) |  |  |
| model | [string](#string) |  |  |
| proxy | [string](#string) |  |  |
| max_tokens | [int32](#int32) |  |  |
| context_window | [int32](#int32) |  |  |
| timeout | [int32](#int32) |  |  |
| images | [bool](#bool) | optional |  |






<a name="cyber-config-LLMProviderView"></a>

### LLMProviderView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| name | [string](#string) |  |  |
| provider | [string](#string) |  |  |
| base_url | [string](#string) |  |  |
| api_key_configured | [bool](#bool) |  |  |
| model | [string](#string) |  |  |
| proxy | [string](#string) |  |  |
| max_tokens | [int32](#int32) |  |  |
| context_window | [int32](#int32) |  |  |
| timeout | [int32](#int32) |  |  |
| images | [bool](#bool) | optional |  |






<a name="cyber-config-LLMView"></a>

### LLMView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| active_profile | [string](#string) |  |  |
| active | [LLMProviderView](#cyber-config-LLMProviderView) |  |  |
| providers | [LLMProviderView](#cyber-config-LLMProviderView) | repeated |  |






<a name="cyber-config-ListModelsResult"></a>

### ListModelsResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ok | [bool](#bool) |  |  |
| supported | [bool](#bool) |  |  |
| models | [string](#string) | repeated |  |
| error | [string](#string) |  |  |






<a name="cyber-config-ReconConfig"></a>

### ReconConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| fofa_key | [string](#string) |  |  |
| hunter_api_key | [string](#string) |  |  |
| proxy | [string](#string) |  |  |
| limit | [int32](#int32) |  |  |






<a name="cyber-config-ReconView"></a>

### ReconView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| fofa_key_configured | [bool](#bool) |  |  |
| hunter_api_key_configured | [bool](#bool) |  |  |
| proxy | [string](#string) |  |  |
| limit | [int32](#int32) |  |  |






<a name="cyber-config-ScanConfig"></a>

### ScanConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| verify | [string](#string) |  |  |






<a name="cyber-config-SearchConfig"></a>

### SearchConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tavily_keys | [string](#string) |  |  |






<a name="cyber-config-SearchView"></a>

### SearchView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tavily_keys_configured | [bool](#bool) |  |  |






<a name="cyber-config-TestConnectionRequest"></a>

### TestConnectionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| section | [string](#string) |  |  |
| config | [DistributeConfig](#cyber-config-DistributeConfig) |  |  |






<a name="cyber-config-TestConnectionResponse"></a>

### TestConnectionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| checks | [ConnectionCheck](#cyber-config-ConnectionCheck) | repeated |  |






<a name="cyber-config-UpdateConfigRequest"></a>

### UpdateConfigRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [DistributeConfig](#cyber-config-DistributeConfig) |  |  |






<a name="cyber-config-UpdateConfigResponse"></a>

### UpdateConfigResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [ConfigView](#cyber-config-ConfigView) |  |  |















<a name="types_reload-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/reload.proto



<a name="cyber-reload-ReloadProtocolMessage"></a>

### ReloadProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request | [ReloadRequest](#cyber-reload-ReloadRequest) |  |  |
| result | [ReloadResult](#cyber-reload-ReloadResult) |  |  |






<a name="cyber-reload-ReloadRequest"></a>

### ReloadRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [cyber.config.DistributeConfig](#cyber-config-DistributeConfig) |  |  |






<a name="cyber-reload-ReloadResult"></a>

### ReloadResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ok | [bool](#bool) |  |  |
| provider | [string](#string) |  |  |
| model | [string](#string) |  |  |
| error | [string](#string) |  |  |















<a name="types_scan-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/scan.proto



<a name="cyber-scan-CancelScanRequest"></a>

### CancelScanRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| scan_id | [string](#string) |  |  |






<a name="cyber-scan-CancelScanResponse"></a>

### CancelScanResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| accepted | [Scan](#cyber-scan-Scan) |  |  |
| rejected | [aop.Rejection](#aop-Rejection) |  |  |






<a name="cyber-scan-GetScanReportRequest"></a>

### GetScanReportRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |
| language | [string](#string) |  |  |






<a name="cyber-scan-GetScanReportResponse"></a>

### GetScanReportResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| markdown | [string](#string) |  |  |
| media_type | [string](#string) |  |  |






<a name="cyber-scan-GetScanRequest"></a>

### GetScanRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |






<a name="cyber-scan-GetScanResponse"></a>

### GetScanResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan | [Scan](#cyber-scan-Scan) |  |  |






<a name="cyber-scan-ListScansRequest"></a>

### ListScansRequest







<a name="cyber-scan-ListScansResponse"></a>

### ListScansResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scans | [Scan](#cyber-scan-Scan) | repeated |  |






<a name="cyber-scan-Scan"></a>

### Scan



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| target | [string](#string) |  |  |
| mode | [string](#string) |  |  |
| options | [ScanOptions](#cyber-scan-ScanOptions) |  |  |
| status | [ScanStatus](#cyber-scan-ScanStatus) |  |  |
| progress | [string](#string) |  |  |
| report | [string](#string) |  |  |
| error | [string](#string) |  |  |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="cyber-scan-ScanCompleted"></a>

### ScanCompleted







<a name="cyber-scan-ScanEvent"></a>

### ScanEvent



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |
| sequence | [uint64](#uint64) |  |  |
| emitted_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| snapshot | [Scan](#cyber-scan-Scan) |  |  |
| status | [ScanStatus](#cyber-scan-ScanStatus) |  |  |
| progress | [ScanProgress](#cyber-scan-ScanProgress) |  |  |
| completed | [ScanCompleted](#cyber-scan-ScanCompleted) |  |  |
| failed | [ScanFailed](#cyber-scan-ScanFailed) |  |  |






<a name="cyber-scan-ScanFailed"></a>

### ScanFailed



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| message | [string](#string) |  |  |
| canceled | [bool](#bool) |  |  |






<a name="cyber-scan-ScanOptions"></a>

### ScanOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| verify | [bool](#bool) |  |  |
| sniper | [bool](#bool) |  |  |
| deep | [bool](#bool) |  |  |






<a name="cyber-scan-ScanProgress"></a>

### ScanProgress



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [string](#string) |  |  |






<a name="cyber-scan-ScanProtocolMessage"></a>

### ScanProtocolMessage
ProtocolMessage carries Cyber scan runtime semantics over the shared AOP
WebSocket. Scan management remains on ScanService.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| watch_events_request | [WatchScanEventsRequest](#cyber-scan-WatchScanEventsRequest) |  |  |
| event | [ScanEvent](#cyber-scan-ScanEvent) |  |  |






<a name="cyber-scan-SessionBinding"></a>

### SessionBinding
SessionBinding attaches an Cyber Scan to an AOP Session at open time.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |






<a name="cyber-scan-SessionScanEvent"></a>

### SessionScanEvent
SessionScanEvent links a completed scan into an AOP session timeline without
reintroducing a parallel web-only domain event envelope.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |
| status | [ScanStatus](#cyber-scan-ScanStatus) |  |  |






<a name="cyber-scan-SubmitScanRequest"></a>

### SubmitScanRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| target | [string](#string) |  |  |
| mode | [string](#string) |  |  |
| options | [ScanOptions](#cyber-scan-ScanOptions) |  |  |






<a name="cyber-scan-SubmitScanResponse"></a>

### SubmitScanResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| accepted | [Scan](#cyber-scan-Scan) |  |  |
| rejected | [aop.Rejection](#aop-Rejection) |  |  |






<a name="cyber-scan-WatchScanEventsRequest"></a>

### WatchScanEventsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |








<a name="cyber-scan-ScanStatus"></a>

### ScanStatus


| Name | Number | Description |
| ---- | ------ | ----------- |
| SCAN_STATUS_UNSPECIFIED | 0 |  |
| SCAN_STATUS_QUEUED | 1 |  |
| SCAN_STATUS_RUNNING | 2 |  |
| SCAN_STATUS_COMPLETED | 3 |  |
| SCAN_STATUS_FAILED | 4 |  |
| SCAN_STATUS_CANCELED | 5 |  |










<a name="types_sco-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/sco.proto



<a name="cyber-sco-DeleteNodesRequest"></a>

### DeleteNodesRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| operation_id | [string](#string) |  |  |






<a name="cyber-sco-DeleteNodesResponse"></a>

### DeleteNodesResponse







<a name="cyber-sco-GetNodeRequest"></a>

### GetNodeRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="cyber-sco-GetNodeResponse"></a>

### GetNodeResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| node | [bytes](#bytes) |  |  |
| media_type | [string](#string) |  |  |






<a name="cyber-sco-GetStatsRequest"></a>

### GetStatsRequest







<a name="cyber-sco-GetStatsResponse"></a>

### GetStatsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| values | [GetStatsResponse.ValuesEntry](#cyber-sco-GetStatsResponse-ValuesEntry) | repeated |  |






<a name="cyber-sco-GetStatsResponse-ValuesEntry"></a>

### GetStatsResponse.ValuesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [uint64](#uint64) |  |  |






<a name="cyber-sco-ImportNodesRequest"></a>

### ImportNodesRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [bytes](#bytes) |  |  |
| artifact | [string](#string) |  |  |
| operation_id | [string](#string) |  |  |






<a name="cyber-sco-ImportNodesResponse"></a>

### ImportNodesResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| nodes | [uint64](#uint64) |  |  |
| duplicates | [uint64](#uint64) |  |  |
| artifact | [string](#string) |  |  |






<a name="cyber-sco-ListArtifactsRequest"></a>

### ListArtifactsRequest







<a name="cyber-sco-ListArtifactsResponse"></a>

### ListArtifactsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| artifacts | [string](#string) | repeated |  |






<a name="cyber-sco-ListNodesRequest"></a>

### ListNodesRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| type | [string](#string) |  |  |
| operation_id | [string](#string) |  |  |
| limit | [uint32](#uint32) |  |  |






<a name="cyber-sco-ListNodesResponse"></a>

### ListNodesResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| nodes | [aop.sco.Nodes](#aop-sco-Nodes) |  |  |















<a name="types_system-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/system.proto



<a name="cyber-system-GetStatusRequest"></a>

### GetStatusRequest







<a name="cyber-system-GetStatusResponse"></a>

### GetStatusResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| status | [SystemStatus](#cyber-system-SystemStatus) |  |  |






<a name="cyber-system-SystemStatus"></a>

### SystemStatus



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| version | [string](#string) |  |  |
| llm_available | [bool](#bool) |  |  |
| llm_provider | [string](#string) |  |  |
| llm_model | [string](#string) |  |  |
| llm_api_key_configured | [bool](#bool) |  |  |
| config_path | [string](#string) |  |  |
| config_loaded | [bool](#bool) |  |  |
| agents | [uint32](#uint32) |  |  |
| server_url | [string](#string) |  |  |















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
