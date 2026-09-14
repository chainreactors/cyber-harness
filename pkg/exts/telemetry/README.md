# Telemetry extension

Telemetry consumes the profile's AOP EventBus and persists events using the
configured JSONL writer. It owns output policy and file lifecycle; EventBus
owns only delivery and subscription draining. AOP protobuf types are shared
protocol types and are not duplicated as DTOs.

`pkg/exts/eventoutput` is the legacy implementation package. New composition
roots must import `pkg/exts/telemetry`.
