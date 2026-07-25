import type {
  AOPData,
  AOPEvent as CyberAOPEvent,
} from '../../cyber-ui/packages/agent-protocol/src'

export * from '../../cyber-ui/packages/agent-protocol/src'

export interface AOPEvent<T extends AOPData = AOPData> extends CyberAOPEvent<T> {
  turn_id?: string
}

export interface MessageImagePart {
  base64?: string
  media_type?: string
  path?: string
}

export interface MessagePart {
  type: string
  text?: string
  image?: MessageImagePart
}

export interface MessageData {
  message_id: string
  role?: string
  parts: MessagePart[]
}

export interface MessageDeltaData {
  message_id: string
  delta: string
  part_type?: string
}
