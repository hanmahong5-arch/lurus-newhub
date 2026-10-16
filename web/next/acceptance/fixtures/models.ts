/**
 * Every wire identifier the instance's price table and routing key on, in one
 * place. They are values the product matches by exact string (a model id in a
 * request body, a channel name in the operator's list), so they cannot be
 * renamed in the suite alone; scenarios import these constants and never spell
 * the literals themselves.
 */

/** The chat model the fake vendor serves: priced per token, output dearer. */
export const CHAT_MODEL = 'deepseek-chat';

/** The hosted decision model: input priced, output free. */
export const HOSTED_MODEL = 'jev-latest';

/**
 * The hosted family. A stored price table that predates it holds none of
 * these rows, which is the shape TC-P1 reproduces.
 */
export const HOSTED_FAMILY = [
  HOSTED_MODEL,
  'jev-preview',
  'jev-1.13.0',
  'laya-auto',
  'laya-english',
  'laya-multilingual',
  'laya-typed-decisions',
] as const;

/** Route of the hosted decision wire. */
export const HOSTED_PATH = '/v1/systemone';

/** Operator-visible channel names; world.setup is idempotent on them. */
export const CHANNEL_NAMES = {
  chat: 'accept-deepseek',
  chatSecond: 'accept-deepseek-b',
  hosted: 'accept-typesafe',
} as const;
