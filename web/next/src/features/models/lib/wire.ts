/**
 * Backend supported_endpoint_types wire values (see the Go side's
 * constant.EndpointType*). These are protocol identifiers the server sends,
 * so they are matched verbatim.
 */
export const WIRE_CHAT = 'openai';
export const WIRE_RESPONSES = 'openai-response';
export const WIRE_MESSAGES = 'anthropic';
export const WIRE_GENERATE = 'gemini';
export const WIRE_VIDEO = 'openai-video';
