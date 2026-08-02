# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [rpc/agent.proto](#rpc_agent-proto)
    - [AgentService](#aiscan-rpc-agent-AgentService)
  
- [rpc/chat.proto](#rpc_chat-proto)
    - [SessionService](#aiscan-rpc-chat-SessionService)
  
- [rpc/config.proto](#rpc_config-proto)
    - [ConfigService](#aiscan-rpc-config-ConfigService)
  
- [rpc/scan.proto](#rpc_scan-proto)
    - [ScanService](#aiscan-rpc-scan-ScanService)
  
- [rpc/sco.proto](#rpc_sco-proto)
    - [SCOService](#aiscan-rpc-sco-SCOService)
  
- [rpc/system.proto](#rpc_system-proto)
    - [SystemService](#aiscan-rpc-system-SystemService)
  
- [types/agent.proto](#types_agent-proto)
    - [AgentListEntry](#aiscan-agent-AgentListEntry)
    - [AgentListMetadata](#aiscan-agent-AgentListMetadata)
    - [AgentRunOptions](#aiscan-agent-AgentRunOptions)
    - [AgentView](#aiscan-agent-AgentView)
    - [BudgetWarning](#aiscan-agent-BudgetWarning)
    - [CommandDetail](#aiscan-agent-CommandDetail)
    - [CompactDetail](#aiscan-agent-CompactDetail)
    - [DelegationDetail](#aiscan-agent-DelegationDetail)
    - [EvalControl](#aiscan-agent-EvalControl)
    - [EvalDetail](#aiscan-agent-EvalDetail)
    - [LLMRequestDetail](#aiscan-agent-LLMRequestDetail)
    - [ListAgentsRequest](#aiscan-agent-ListAgentsRequest)
    - [ListAgentsResponse](#aiscan-agent-ListAgentsResponse)
    - [WebMessageMetadata](#aiscan-agent-WebMessageMetadata)
  
- [types/chat.proto](#types_chat-proto)
    - [DeleteSessionRequest](#aiscan-chat-DeleteSessionRequest)
    - [DeleteSessionResponse](#aiscan-chat-DeleteSessionResponse)
    - [GetSessionRequest](#aiscan-chat-GetSessionRequest)
    - [GetSessionResponse](#aiscan-chat-GetSessionResponse)
    - [ListCommandsRequest](#aiscan-chat-ListCommandsRequest)
    - [ListCommandsResponse](#aiscan-chat-ListCommandsResponse)
    - [ListSessionsRequest](#aiscan-chat-ListSessionsRequest)
    - [ListSessionsResponse](#aiscan-chat-ListSessionsResponse)
    - [ResetSessionReceipt](#aiscan-chat-ResetSessionReceipt)
    - [ResetSessionRequest](#aiscan-chat-ResetSessionRequest)
    - [ResetSessionResponse](#aiscan-chat-ResetSessionResponse)
    - [SessionRecord](#aiscan-chat-SessionRecord)
  
- [types/command.proto](#types_command-proto)
    - [CommandCatalog](#aiscan-command-CommandCatalog)
    - [CommandProtocolMessage](#aiscan-command-CommandProtocolMessage)
    - [CommandReceipt](#aiscan-command-CommandReceipt)
    - [CommandRequest](#aiscan-command-CommandRequest)
    - [CommandResult](#aiscan-command-CommandResult)
    - [CommandSpec](#aiscan-command-CommandSpec)
  
- [types/config.proto](#types_config-proto)
    - [ActivateProfileRequest](#aiscan-config-ActivateProfileRequest)
    - [ActivateProfileResponse](#aiscan-config-ActivateProfileResponse)
    - [AgentConfig](#aiscan-config-AgentConfig)
    - [ConfigView](#aiscan-config-ConfigView)
    - [ConnectionCheck](#aiscan-config-ConnectionCheck)
    - [CyberhubConfig](#aiscan-config-CyberhubConfig)
    - [CyberhubView](#aiscan-config-CyberhubView)
    - [DistributeConfig](#aiscan-config-DistributeConfig)
    - [GetConfigRequest](#aiscan-config-GetConfigRequest)
    - [GetConfigResponse](#aiscan-config-GetConfigResponse)
    - [IOAConfig](#aiscan-config-IOAConfig)
    - [IOAView](#aiscan-config-IOAView)
    - [LLMConfig](#aiscan-config-LLMConfig)
    - [LLMProbeRequest](#aiscan-config-LLMProbeRequest)
    - [LLMProbeResult](#aiscan-config-LLMProbeResult)
    - [LLMProviderConfig](#aiscan-config-LLMProviderConfig)
    - [LLMProviderView](#aiscan-config-LLMProviderView)
    - [LLMView](#aiscan-config-LLMView)
    - [ListModelsResult](#aiscan-config-ListModelsResult)
    - [ReconConfig](#aiscan-config-ReconConfig)
    - [ReconView](#aiscan-config-ReconView)
    - [ScanConfig](#aiscan-config-ScanConfig)
    - [SearchConfig](#aiscan-config-SearchConfig)
    - [SearchView](#aiscan-config-SearchView)
    - [TestConnectionRequest](#aiscan-config-TestConnectionRequest)
    - [TestConnectionResponse](#aiscan-config-TestConnectionResponse)
    - [UpdateConfigRequest](#aiscan-config-UpdateConfigRequest)
    - [UpdateConfigResponse](#aiscan-config-UpdateConfigResponse)
  
- [types/reload.proto](#types_reload-proto)
    - [ReloadProtocolMessage](#aiscan-reload-ReloadProtocolMessage)
    - [ReloadRequest](#aiscan-reload-ReloadRequest)
    - [ReloadResult](#aiscan-reload-ReloadResult)
  
- [types/scan.proto](#types_scan-proto)
    - [CancelScanRequest](#aiscan-scan-CancelScanRequest)
    - [CancelScanResponse](#aiscan-scan-CancelScanResponse)
    - [GetScanReportRequest](#aiscan-scan-GetScanReportRequest)
    - [GetScanReportResponse](#aiscan-scan-GetScanReportResponse)
    - [GetScanRequest](#aiscan-scan-GetScanRequest)
    - [GetScanResponse](#aiscan-scan-GetScanResponse)
    - [ListScansRequest](#aiscan-scan-ListScansRequest)
    - [ListScansResponse](#aiscan-scan-ListScansResponse)
    - [Scan](#aiscan-scan-Scan)
    - [ScanCompleted](#aiscan-scan-ScanCompleted)
    - [ScanEvent](#aiscan-scan-ScanEvent)
    - [ScanFailed](#aiscan-scan-ScanFailed)
    - [ScanOptions](#aiscan-scan-ScanOptions)
    - [ScanProgress](#aiscan-scan-ScanProgress)
    - [ScanProtocolMessage](#aiscan-scan-ScanProtocolMessage)
    - [SessionBinding](#aiscan-scan-SessionBinding)
    - [SessionScanEvent](#aiscan-scan-SessionScanEvent)
    - [SubmitScanRequest](#aiscan-scan-SubmitScanRequest)
    - [SubmitScanResponse](#aiscan-scan-SubmitScanResponse)
    - [WatchScanEventsRequest](#aiscan-scan-WatchScanEventsRequest)
  
    - [ScanStatus](#aiscan-scan-ScanStatus)
  
- [types/sco.proto](#types_sco-proto)
    - [DeleteNodesRequest](#aiscan-sco-DeleteNodesRequest)
    - [DeleteNodesResponse](#aiscan-sco-DeleteNodesResponse)
    - [GetNodeRequest](#aiscan-sco-GetNodeRequest)
    - [GetNodeResponse](#aiscan-sco-GetNodeResponse)
    - [GetStatsRequest](#aiscan-sco-GetStatsRequest)
    - [GetStatsResponse](#aiscan-sco-GetStatsResponse)
    - [GetStatsResponse.ValuesEntry](#aiscan-sco-GetStatsResponse-ValuesEntry)
    - [ImportNodesRequest](#aiscan-sco-ImportNodesRequest)
    - [ImportNodesResponse](#aiscan-sco-ImportNodesResponse)
    - [ListArtifactsRequest](#aiscan-sco-ListArtifactsRequest)
    - [ListArtifactsResponse](#aiscan-sco-ListArtifactsResponse)
    - [ListNodesRequest](#aiscan-sco-ListNodesRequest)
    - [ListNodesResponse](#aiscan-sco-ListNodesResponse)
  
- [types/system.proto](#types_system-proto)
    - [GetStatusRequest](#aiscan-system-GetStatusRequest)
    - [GetStatusResponse](#aiscan-system-GetStatusResponse)
    - [SystemStatus](#aiscan-system-SystemStatus)
  
- [Scalar Value Types](#scalar-value-types)



<a name="rpc_agent-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/agent.proto


 

 

 


<a name="aiscan-rpc-agent-AgentService"></a>

### AgentService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| ListAgents | [.aiscan.agent.ListAgentsRequest](#aiscan-agent-ListAgentsRequest) | [.aiscan.agent.ListAgentsResponse](#aiscan-agent-ListAgentsResponse) |  |

 



<a name="rpc_chat-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/chat.proto


 

 

 


<a name="aiscan-rpc-chat-SessionService"></a>

### SessionService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| ListSessions | [.aiscan.chat.ListSessionsRequest](#aiscan-chat-ListSessionsRequest) | [.aiscan.chat.ListSessionsResponse](#aiscan-chat-ListSessionsResponse) |  |
| GetSession | [.aiscan.chat.GetSessionRequest](#aiscan-chat-GetSessionRequest) | [.aiscan.chat.GetSessionResponse](#aiscan-chat-GetSessionResponse) |  |
| ResetSession | [.aiscan.chat.ResetSessionRequest](#aiscan-chat-ResetSessionRequest) | [.aiscan.chat.ResetSessionResponse](#aiscan-chat-ResetSessionResponse) |  |
| DeleteSession | [.aiscan.chat.DeleteSessionRequest](#aiscan-chat-DeleteSessionRequest) | [.aiscan.chat.DeleteSessionResponse](#aiscan-chat-DeleteSessionResponse) |  |
| ListCommands | [.aiscan.chat.ListCommandsRequest](#aiscan-chat-ListCommandsRequest) | [.aiscan.chat.ListCommandsResponse](#aiscan-chat-ListCommandsResponse) |  |
| ListEvents | [.aop.ListEventsRequest](#aop-ListEventsRequest) | [.aop.ListEventsResponse](#aop-ListEventsResponse) |  |

 



<a name="rpc_config-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/config.proto


 

 

 


<a name="aiscan-rpc-config-ConfigService"></a>

### ConfigService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| GetConfig | [.aiscan.config.GetConfigRequest](#aiscan-config-GetConfigRequest) | [.aiscan.config.GetConfigResponse](#aiscan-config-GetConfigResponse) |  |
| UpdateConfig | [.aiscan.config.UpdateConfigRequest](#aiscan-config-UpdateConfigRequest) | [.aiscan.config.UpdateConfigResponse](#aiscan-config-UpdateConfigResponse) |  |
| ActivateProfile | [.aiscan.config.ActivateProfileRequest](#aiscan-config-ActivateProfileRequest) | [.aiscan.config.ActivateProfileResponse](#aiscan-config-ActivateProfileResponse) |  |
| TestLLM | [.aiscan.config.LLMProbeRequest](#aiscan-config-LLMProbeRequest) | [.aiscan.config.LLMProbeResult](#aiscan-config-LLMProbeResult) |  |
| ListModels | [.aiscan.config.LLMProbeRequest](#aiscan-config-LLMProbeRequest) | [.aiscan.config.ListModelsResult](#aiscan-config-ListModelsResult) |  |
| TestConnection | [.aiscan.config.TestConnectionRequest](#aiscan-config-TestConnectionRequest) | [.aiscan.config.TestConnectionResponse](#aiscan-config-TestConnectionResponse) |  |

 



<a name="rpc_scan-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/scan.proto


 

 

 


<a name="aiscan-rpc-scan-ScanService"></a>

### ScanService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| SubmitScan | [.aiscan.scan.SubmitScanRequest](#aiscan-scan-SubmitScanRequest) | [.aiscan.scan.SubmitScanResponse](#aiscan-scan-SubmitScanResponse) |  |
| GetScan | [.aiscan.scan.GetScanRequest](#aiscan-scan-GetScanRequest) | [.aiscan.scan.GetScanResponse](#aiscan-scan-GetScanResponse) |  |
| ListScans | [.aiscan.scan.ListScansRequest](#aiscan-scan-ListScansRequest) | [.aiscan.scan.ListScansResponse](#aiscan-scan-ListScansResponse) |  |
| CancelScan | [.aiscan.scan.CancelScanRequest](#aiscan-scan-CancelScanRequest) | [.aiscan.scan.CancelScanResponse](#aiscan-scan-CancelScanResponse) |  |
| GetScanReport | [.aiscan.scan.GetScanReportRequest](#aiscan-scan-GetScanReportRequest) | [.aiscan.scan.GetScanReportResponse](#aiscan-scan-GetScanReportResponse) |  |

 



<a name="rpc_sco-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/sco.proto


 

 

 


<a name="aiscan-rpc-sco-SCOService"></a>

### SCOService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| ListNodes | [.aiscan.sco.ListNodesRequest](#aiscan-sco-ListNodesRequest) | [.aiscan.sco.ListNodesResponse](#aiscan-sco-ListNodesResponse) |  |
| GetNode | [.aiscan.sco.GetNodeRequest](#aiscan-sco-GetNodeRequest) | [.aiscan.sco.GetNodeResponse](#aiscan-sco-GetNodeResponse) |  |
| GetStats | [.aiscan.sco.GetStatsRequest](#aiscan-sco-GetStatsRequest) | [.aiscan.sco.GetStatsResponse](#aiscan-sco-GetStatsResponse) |  |
| DeleteNodes | [.aiscan.sco.DeleteNodesRequest](#aiscan-sco-DeleteNodesRequest) | [.aiscan.sco.DeleteNodesResponse](#aiscan-sco-DeleteNodesResponse) |  |
| ImportNodes | [.aiscan.sco.ImportNodesRequest](#aiscan-sco-ImportNodesRequest) | [.aiscan.sco.ImportNodesResponse](#aiscan-sco-ImportNodesResponse) |  |
| ListArtifacts | [.aiscan.sco.ListArtifactsRequest](#aiscan-sco-ListArtifactsRequest) | [.aiscan.sco.ListArtifactsResponse](#aiscan-sco-ListArtifactsResponse) |  |

 



<a name="rpc_system-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## rpc/system.proto


 

 

 


<a name="aiscan-rpc-system-SystemService"></a>

### SystemService


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| GetStatus | [.aiscan.system.GetStatusRequest](#aiscan-system-GetStatusRequest) | [.aiscan.system.GetStatusResponse](#aiscan-system-GetStatusResponse) |  |

 



<a name="types_agent-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/agent.proto



<a name="aiscan-agent-AgentListEntry"></a>

### AgentListEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| node_id | [string](#string) |  |  |
| busy | [bool](#bool) |  |  |
| provider | [string](#string) |  |  |
| model | [string](#string) |  |  |






<a name="aiscan-agent-AgentListMetadata"></a>

### AgentListMetadata



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| agents | [AgentListEntry](#aiscan-agent-AgentListEntry) | repeated |  |






<a name="aiscan-agent-AgentRunOptions"></a>

### AgentRunOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| eval_criteria | [string](#string) |  |  |
| eval_max_rounds | [uint32](#uint32) |  |  |






<a name="aiscan-agent-AgentView"></a>

### AgentView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| hello | [aop.AgentHello](#aop-AgentHello) |  |  |
| status | [aop.AgentStatus](#aop-AgentStatus) |  |  |
| stats | [aop.AgentStats](#aop-AgentStats) |  |  |
| connected_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| commands | [aiscan.command.CommandSpec](#aiscan-command-CommandSpec) | repeated |  |
| busy | [bool](#bool) |  |  |






<a name="aiscan-agent-BudgetWarning"></a>

### BudgetWarning



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| context_tokens | [uint64](#uint64) |  |  |
| token_budget | [uint64](#uint64) |  |  |






<a name="aiscan-agent-CommandDetail"></a>

### CommandDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| line | [string](#string) |  |  |
| presentation | [string](#string) |  |  |






<a name="aiscan-agent-CompactDetail"></a>

### CompactDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| error | [string](#string) |  |  |
| kept_messages | [uint64](#uint64) |  |  |
| tokens_after | [uint64](#uint64) |  |  |
| tokens_before | [uint64](#uint64) |  |  |






<a name="aiscan-agent-DelegationDetail"></a>

### DelegationDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| agent_id | [string](#string) |  |  |
| agent_name | [string](#string) |  |  |
| agent_type | [string](#string) |  |  |
| context_mode | [string](#string) |  |  |
| run_mode | [string](#string) |  |  |
| task | [string](#string) |  |  |






<a name="aiscan-agent-EvalControl"></a>

### EvalControl



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| criteria | [string](#string) |  |  |
| max_rounds | [uint32](#uint32) |  |  |






<a name="aiscan-agent-EvalDetail"></a>

### EvalDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| error | [string](#string) |  |  |
| max_rounds | [uint32](#uint32) |  |  |
| pass | [bool](#bool) |  |  |
| reason | [string](#string) |  |  |
| round | [uint32](#uint32) |  |  |






<a name="aiscan-agent-LLMRequestDetail"></a>

### LLMRequestDetail



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| model | [string](#string) |  |  |
| messages | [uint32](#uint32) |  |  |
| max_tokens | [uint32](#uint32) |  |  |
| stream | [bool](#bool) |  |  |






<a name="aiscan-agent-ListAgentsRequest"></a>

### ListAgentsRequest







<a name="aiscan-agent-ListAgentsResponse"></a>

### ListAgentsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| agents | [AgentView](#aiscan-agent-AgentView) | repeated |  |






<a name="aiscan-agent-WebMessageMetadata"></a>

### WebMessageMetadata



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| node_id | [string](#string) |  |  |
| code | [string](#string) |  |  |
| params | [google.protobuf.Struct](#google-protobuf-Struct) |  |  |
| agent_list | [AgentListMetadata](#aiscan-agent-AgentListMetadata) |  |  |





 

 

 

 



<a name="types_chat-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/chat.proto



<a name="aiscan-chat-DeleteSessionRequest"></a>

### DeleteSessionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| session_id | [string](#string) |  |  |






<a name="aiscan-chat-DeleteSessionResponse"></a>

### DeleteSessionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| accepted | [aop.Session](#aop-Session) |  |  |
| rejected | [aop.Rejection](#aop-Rejection) |  |  |






<a name="aiscan-chat-GetSessionRequest"></a>

### GetSessionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |






<a name="aiscan-chat-GetSessionResponse"></a>

### GetSessionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session | [SessionRecord](#aiscan-chat-SessionRecord) |  |  |






<a name="aiscan-chat-ListCommandsRequest"></a>

### ListCommandsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |






<a name="aiscan-chat-ListCommandsResponse"></a>

### ListCommandsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| commands | [aiscan.command.CommandSpec](#aiscan-command-CommandSpec) | repeated |  |






<a name="aiscan-chat-ListSessionsRequest"></a>

### ListSessionsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| after_cursor | [string](#string) |  |  |
| limit | [uint32](#uint32) |  |  |
| include_closed | [bool](#bool) |  |  |






<a name="aiscan-chat-ListSessionsResponse"></a>

### ListSessionsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sessions | [SessionRecord](#aiscan-chat-SessionRecord) | repeated |  |
| next_cursor | [string](#string) |  |  |






<a name="aiscan-chat-ResetSessionReceipt"></a>

### ResetSessionReceipt



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| previous | [aop.Session](#aop-Session) |  |  |
| current | [SessionRecord](#aiscan-chat-SessionRecord) |  |  |






<a name="aiscan-chat-ResetSessionRequest"></a>

### ResetSessionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| session_id | [string](#string) |  |  |
| new_session_id | [string](#string) |  |  |
| title | [string](#string) |  |  |






<a name="aiscan-chat-ResetSessionResponse"></a>

### ResetSessionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| accepted | [ResetSessionReceipt](#aiscan-chat-ResetSessionReceipt) |  |  |
| rejected | [aop.Rejection](#aop-Rejection) |  |  |






<a name="aiscan-chat-SessionRecord"></a>

### SessionRecord



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session | [aop.Session](#aop-Session) |  |  |
| agent_name | [string](#string) |  |  |
| scan_ids | [string](#string) | repeated |  |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 

 

 

 



<a name="types_command-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/command.proto



<a name="aiscan-command-CommandCatalog"></a>

### CommandCatalog



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| commands | [CommandSpec](#aiscan-command-CommandSpec) | repeated |  |






<a name="aiscan-command-CommandProtocolMessage"></a>

### CommandProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request | [CommandRequest](#aiscan-command-CommandRequest) |  |  |
| result | [CommandResult](#aiscan-command-CommandResult) |  |  |
| catalog | [CommandCatalog](#aiscan-command-CommandCatalog) |  |  |
| receipt | [CommandReceipt](#aiscan-command-CommandReceipt) |  |  |






<a name="aiscan-command-CommandReceipt"></a>

### CommandReceipt



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| operation_id | [string](#string) |  |  |
| session_id | [string](#string) |  |  |
| state | [string](#string) |  |  |






<a name="aiscan-command-CommandRequest"></a>

### CommandRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| session_id | [string](#string) |  |  |
| line | [string](#string) |  |  |






<a name="aiscan-command-CommandResult"></a>

### CommandResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| command | [string](#string) |  |  |
| presentation | [string](#string) |  |  |
| content | [aop.Content](#aop-Content) | repeated |  |






<a name="aiscan-command-CommandSpec"></a>

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



<a name="aiscan-config-ActivateProfileRequest"></a>

### ActivateProfileRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| profile_id | [string](#string) |  |  |






<a name="aiscan-config-ActivateProfileResponse"></a>

### ActivateProfileResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [ConfigView](#aiscan-config-ConfigView) |  |  |






<a name="aiscan-config-AgentConfig"></a>

### AgentConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tools | [string](#string) | repeated |  |
| timeout | [int32](#int32) |  |  |
| save_session | [bool](#bool) |  |  |






<a name="aiscan-config-ConfigView"></a>

### ConfigView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| path | [string](#string) |  |  |
| loaded | [bool](#bool) |  |  |
| llm | [LLMView](#aiscan-config-LLMView) |  |  |
| cyberhub | [CyberhubView](#aiscan-config-CyberhubView) |  |  |
| recon | [ReconView](#aiscan-config-ReconView) |  |  |
| scan | [ScanConfig](#aiscan-config-ScanConfig) |  |  |
| search | [SearchView](#aiscan-config-SearchView) |  |  |
| ioa | [IOAView](#aiscan-config-IOAView) |  |  |
| agent | [AgentConfig](#aiscan-config-AgentConfig) |  |  |






<a name="aiscan-config-ConnectionCheck"></a>

### ConnectionCheck



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| ok | [bool](#bool) |  |  |
| latency_ms | [int64](#int64) |  |  |
| detail | [string](#string) |  |  |
| error | [string](#string) |  |  |






<a name="aiscan-config-CyberhubConfig"></a>

### CyberhubConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| url | [string](#string) |  |  |
| key | [string](#string) |  |  |
| mode | [string](#string) |  |  |
| proxy | [string](#string) |  |  |






<a name="aiscan-config-CyberhubView"></a>

### CyberhubView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| url | [string](#string) |  |  |
| key_configured | [bool](#bool) |  |  |
| mode | [string](#string) |  |  |
| proxy | [string](#string) |  |  |






<a name="aiscan-config-DistributeConfig"></a>

### DistributeConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| llm | [LLMConfig](#aiscan-config-LLMConfig) |  |  |
| cyberhub | [CyberhubConfig](#aiscan-config-CyberhubConfig) |  |  |
| recon | [ReconConfig](#aiscan-config-ReconConfig) |  |  |
| scan | [ScanConfig](#aiscan-config-ScanConfig) |  |  |
| search | [SearchConfig](#aiscan-config-SearchConfig) |  |  |
| ioa | [IOAConfig](#aiscan-config-IOAConfig) |  |  |
| agent | [AgentConfig](#aiscan-config-AgentConfig) |  |  |






<a name="aiscan-config-GetConfigRequest"></a>

### GetConfigRequest







<a name="aiscan-config-GetConfigResponse"></a>

### GetConfigResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [ConfigView](#aiscan-config-ConfigView) |  |  |






<a name="aiscan-config-IOAConfig"></a>

### IOAConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| url | [string](#string) |  |  |
| token | [string](#string) |  |  |
| node_name | [string](#string) |  |  |
| space | [string](#string) |  |  |






<a name="aiscan-config-IOAView"></a>

### IOAView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| url | [string](#string) |  |  |
| token_configured | [bool](#bool) |  |  |
| node_name | [string](#string) |  |  |
| space | [string](#string) |  |  |






<a name="aiscan-config-LLMConfig"></a>

### LLMConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| active_profile | [string](#string) |  |  |
| providers | [LLMProviderConfig](#aiscan-config-LLMProviderConfig) | repeated |  |






<a name="aiscan-config-LLMProbeRequest"></a>

### LLMProbeRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| profile_id | [string](#string) |  |  |
| provider | [string](#string) |  |  |
| base_url | [string](#string) |  |  |
| api_key | [string](#string) |  |  |
| model | [string](#string) |  |  |
| proxy | [string](#string) |  |  |






<a name="aiscan-config-LLMProbeResult"></a>

### LLMProbeResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ok | [bool](#bool) |  |  |
| provider | [string](#string) |  |  |
| model | [string](#string) |  |  |
| latency_ms | [int64](#int64) |  |  |
| reply | [string](#string) |  |  |
| error | [string](#string) |  |  |






<a name="aiscan-config-LLMProviderConfig"></a>

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






<a name="aiscan-config-LLMProviderView"></a>

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






<a name="aiscan-config-LLMView"></a>

### LLMView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| active_profile | [string](#string) |  |  |
| active | [LLMProviderView](#aiscan-config-LLMProviderView) |  |  |
| providers | [LLMProviderView](#aiscan-config-LLMProviderView) | repeated |  |






<a name="aiscan-config-ListModelsResult"></a>

### ListModelsResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ok | [bool](#bool) |  |  |
| supported | [bool](#bool) |  |  |
| models | [string](#string) | repeated |  |
| error | [string](#string) |  |  |






<a name="aiscan-config-ReconConfig"></a>

### ReconConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| fofa_email | [string](#string) |  |  |
| fofa_key | [string](#string) |  |  |
| hunter_token | [string](#string) |  |  |
| hunter_api_key | [string](#string) |  |  |
| proxy | [string](#string) |  |  |
| limit | [int32](#int32) |  |  |






<a name="aiscan-config-ReconView"></a>

### ReconView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| fofa_email | [string](#string) |  |  |
| fofa_key_configured | [bool](#bool) |  |  |
| hunter_token_configured | [bool](#bool) |  |  |
| hunter_api_key_configured | [bool](#bool) |  |  |
| proxy | [string](#string) |  |  |
| limit | [int32](#int32) |  |  |






<a name="aiscan-config-ScanConfig"></a>

### ScanConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| verify | [string](#string) |  |  |






<a name="aiscan-config-SearchConfig"></a>

### SearchConfig



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tavily_keys | [string](#string) |  |  |






<a name="aiscan-config-SearchView"></a>

### SearchView



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tavily_keys_configured | [bool](#bool) |  |  |






<a name="aiscan-config-TestConnectionRequest"></a>

### TestConnectionRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| section | [string](#string) |  |  |
| config | [DistributeConfig](#aiscan-config-DistributeConfig) |  |  |






<a name="aiscan-config-TestConnectionResponse"></a>

### TestConnectionResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| checks | [ConnectionCheck](#aiscan-config-ConnectionCheck) | repeated |  |






<a name="aiscan-config-UpdateConfigRequest"></a>

### UpdateConfigRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [DistributeConfig](#aiscan-config-DistributeConfig) |  |  |






<a name="aiscan-config-UpdateConfigResponse"></a>

### UpdateConfigResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [ConfigView](#aiscan-config-ConfigView) |  |  |





 

 

 

 



<a name="types_reload-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/reload.proto



<a name="aiscan-reload-ReloadProtocolMessage"></a>

### ReloadProtocolMessage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request | [ReloadRequest](#aiscan-reload-ReloadRequest) |  |  |
| result | [ReloadResult](#aiscan-reload-ReloadResult) |  |  |






<a name="aiscan-reload-ReloadRequest"></a>

### ReloadRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| config | [aiscan.config.DistributeConfig](#aiscan-config-DistributeConfig) |  |  |






<a name="aiscan-reload-ReloadResult"></a>

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



<a name="aiscan-scan-CancelScanRequest"></a>

### CancelScanRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| scan_id | [string](#string) |  |  |






<a name="aiscan-scan-CancelScanResponse"></a>

### CancelScanResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| accepted | [Scan](#aiscan-scan-Scan) |  |  |
| rejected | [aop.Rejection](#aop-Rejection) |  |  |






<a name="aiscan-scan-GetScanReportRequest"></a>

### GetScanReportRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |
| language | [string](#string) |  |  |






<a name="aiscan-scan-GetScanReportResponse"></a>

### GetScanReportResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| markdown | [string](#string) |  |  |
| media_type | [string](#string) |  |  |






<a name="aiscan-scan-GetScanRequest"></a>

### GetScanRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |






<a name="aiscan-scan-GetScanResponse"></a>

### GetScanResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan | [Scan](#aiscan-scan-Scan) |  |  |






<a name="aiscan-scan-ListScansRequest"></a>

### ListScansRequest







<a name="aiscan-scan-ListScansResponse"></a>

### ListScansResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scans | [Scan](#aiscan-scan-Scan) | repeated |  |






<a name="aiscan-scan-Scan"></a>

### Scan



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| target | [string](#string) |  |  |
| mode | [string](#string) |  |  |
| options | [ScanOptions](#aiscan-scan-ScanOptions) |  |  |
| status | [ScanStatus](#aiscan-scan-ScanStatus) |  |  |
| progress | [string](#string) |  |  |
| report | [string](#string) |  |  |
| error | [string](#string) |  |  |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="aiscan-scan-ScanCompleted"></a>

### ScanCompleted







<a name="aiscan-scan-ScanEvent"></a>

### ScanEvent



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |
| sequence | [uint64](#uint64) |  |  |
| emitted_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| snapshot | [Scan](#aiscan-scan-Scan) |  |  |
| status | [ScanStatus](#aiscan-scan-ScanStatus) |  |  |
| progress | [ScanProgress](#aiscan-scan-ScanProgress) |  |  |
| completed | [ScanCompleted](#aiscan-scan-ScanCompleted) |  |  |
| failed | [ScanFailed](#aiscan-scan-ScanFailed) |  |  |






<a name="aiscan-scan-ScanFailed"></a>

### ScanFailed



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| message | [string](#string) |  |  |
| canceled | [bool](#bool) |  |  |






<a name="aiscan-scan-ScanOptions"></a>

### ScanOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| verify | [bool](#bool) |  |  |
| sniper | [bool](#bool) |  |  |
| deep | [bool](#bool) |  |  |






<a name="aiscan-scan-ScanProgress"></a>

### ScanProgress



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [string](#string) |  |  |






<a name="aiscan-scan-ScanProtocolMessage"></a>

### ScanProtocolMessage
ProtocolMessage carries AIScan scan runtime semantics over the shared AOP
WebSocket. Scan management remains on ScanService.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| watch_events_request | [WatchScanEventsRequest](#aiscan-scan-WatchScanEventsRequest) |  |  |
| event | [ScanEvent](#aiscan-scan-ScanEvent) |  |  |






<a name="aiscan-scan-SessionBinding"></a>

### SessionBinding
SessionBinding attaches an AIScan Scan to an AOP Session at open time.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |






<a name="aiscan-scan-SessionScanEvent"></a>

### SessionScanEvent
SessionScanEvent links a completed scan into an AOP session timeline without
reintroducing a parallel web-only domain event envelope.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |
| status | [ScanStatus](#aiscan-scan-ScanStatus) |  |  |






<a name="aiscan-scan-SubmitScanRequest"></a>

### SubmitScanRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| target | [string](#string) |  |  |
| mode | [string](#string) |  |  |
| options | [ScanOptions](#aiscan-scan-ScanOptions) |  |  |






<a name="aiscan-scan-SubmitScanResponse"></a>

### SubmitScanResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| request_id | [string](#string) |  |  |
| accepted | [Scan](#aiscan-scan-Scan) |  |  |
| rejected | [aop.Rejection](#aop-Rejection) |  |  |






<a name="aiscan-scan-WatchScanEventsRequest"></a>

### WatchScanEventsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| scan_id | [string](#string) |  |  |





 


<a name="aiscan-scan-ScanStatus"></a>

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



<a name="aiscan-sco-DeleteNodesRequest"></a>

### DeleteNodesRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| operation_id | [string](#string) |  |  |






<a name="aiscan-sco-DeleteNodesResponse"></a>

### DeleteNodesResponse







<a name="aiscan-sco-GetNodeRequest"></a>

### GetNodeRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="aiscan-sco-GetNodeResponse"></a>

### GetNodeResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| node | [bytes](#bytes) |  |  |
| media_type | [string](#string) |  |  |






<a name="aiscan-sco-GetStatsRequest"></a>

### GetStatsRequest







<a name="aiscan-sco-GetStatsResponse"></a>

### GetStatsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| values | [GetStatsResponse.ValuesEntry](#aiscan-sco-GetStatsResponse-ValuesEntry) | repeated |  |






<a name="aiscan-sco-GetStatsResponse-ValuesEntry"></a>

### GetStatsResponse.ValuesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [uint64](#uint64) |  |  |






<a name="aiscan-sco-ImportNodesRequest"></a>

### ImportNodesRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [bytes](#bytes) |  |  |
| artifact | [string](#string) |  |  |
| operation_id | [string](#string) |  |  |






<a name="aiscan-sco-ImportNodesResponse"></a>

### ImportNodesResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| nodes | [uint64](#uint64) |  |  |
| duplicates | [uint64](#uint64) |  |  |
| artifact | [string](#string) |  |  |






<a name="aiscan-sco-ListArtifactsRequest"></a>

### ListArtifactsRequest







<a name="aiscan-sco-ListArtifactsResponse"></a>

### ListArtifactsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| artifacts | [string](#string) | repeated |  |






<a name="aiscan-sco-ListNodesRequest"></a>

### ListNodesRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| type | [string](#string) |  |  |
| operation_id | [string](#string) |  |  |
| limit | [uint32](#uint32) |  |  |






<a name="aiscan-sco-ListNodesResponse"></a>

### ListNodesResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| nodes | [aop.sco.Nodes](#aop-sco-Nodes) |  |  |





 

 

 

 



<a name="types_system-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## types/system.proto



<a name="aiscan-system-GetStatusRequest"></a>

### GetStatusRequest







<a name="aiscan-system-GetStatusResponse"></a>

### GetStatusResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| status | [SystemStatus](#aiscan-system-SystemStatus) |  |  |






<a name="aiscan-system-SystemStatus"></a>

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

