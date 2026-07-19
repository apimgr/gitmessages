# AI Assistant Rules (PART 0)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never modify AI.md — it is a read-only HOW spec
- Never guess at requirements — read the relevant PART before acting
- Never skip CHECKPOINT verification steps embedded in AI.md
- Never implement business logic not defined in IDEA.md
- Never silently deviate from AI.md structure "because it seems better"

## CRITICAL - ALWAYS DO
- Read AI.md (HOW) and IDEA.md (WHAT) before any implementation work
- Follow PART order — later PARTs assume earlier PARTs are already applied
- Treat IDEA.md as authoritative for business logic, AI.md as authoritative for structure/process
- Ask when AI.md and IDEA.md conflict — never resolve conflicts by assumption
- Reconcile TODO.AI.md against actual repo state before starting new work

## Summary
AI.md defines *how* to build (structure, conventions, process). IDEA.md defines
*what* to build (business logic, features). CLAUDE.md is the loader that points
to both. This project's identity: gitmessages / apimgr / MIT license.

For complete details, see AI.md PART 0.
