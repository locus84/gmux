import { z } from 'zod'

/** Browser-only reference replacing one extracted Kitty PNG placement. */
export const TerminalImagePutSchema = z.object({
  version: z.literal(1),
  action: z.literal('put'),
  hash: z.string().regex(/^[0-9a-f]{64}$/),
  id: z.number().int().positive().max(0xffff_ffff),
  cols: z.number().int().positive().max(1000),
  rows: z.number().int().positive().max(1000),
  bytes: z.number().int().positive().max(8 * 1024 * 1024),
  mime: z.literal('image/png'),
}).strict()

/** Deletes placements by Kitty image ID, or every placement in the session. */
export const TerminalImageDeleteSchema = z.union([
  z.object({
    version: z.literal(1),
    action: z.literal('delete'),
    id: z.number().int().positive().max(0xffff_ffff),
  }).strict(),
  z.object({
    version: z.literal(1),
    action: z.literal('delete'),
    all: z.literal(true),
  }).strict(),
])

export const TerminalImageMessageSchema = z.union([
  TerminalImagePutSchema,
  TerminalImageDeleteSchema,
])

export type TerminalImagePut = z.infer<typeof TerminalImagePutSchema>
export type TerminalImageDelete = z.infer<typeof TerminalImageDeleteSchema>
export type TerminalImageMessage = z.infer<typeof TerminalImageMessageSchema>
