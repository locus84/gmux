import { z } from 'zod'
import { responseEnvelope } from './rest.js'

export const WorktreeSchema = z.object({
  path: z.string().min(1),
  head: z.string().optional(),
  branch: z.string().optional(),
  detached: z.boolean().optional().default(false),
  bare: z.boolean().optional().default(false),
  locked: z.boolean().optional().default(false),
  lock_reason: z.string().optional(),
  prunable: z.boolean().optional().default(false),
  primary: z.boolean(),
})

export const ProjectWorktreesSchema = z.object({
  project_slug: z.string().min(1),
  primary_path: z.string().min(1),
  worktrees: z.array(WorktreeSchema),
})

export const ProjectWorktreesResponseSchema = responseEnvelope(ProjectWorktreesSchema)

export type Worktree = z.infer<typeof WorktreeSchema>
export type ProjectWorktrees = z.infer<typeof ProjectWorktreesSchema>
export type ProjectWorktreesResponse = z.infer<typeof ProjectWorktreesResponseSchema>
