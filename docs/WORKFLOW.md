# Working Workflow

This repository must follow an approval-driven workflow.

## Required Loop

For each meaningful feature or design decision:

1. Discuss the implementation shape before coding.
2. Present tradeoffs in a form the owner can evaluate.
3. Wait for owner approval.
4. Implement only the approved approach.
5. Run QA or verification appropriate to the change.
6. Present the implementation and QA result in a clear owner-facing summary.

## Discussion Before Implementation

Before coding, the agent should explain:

- What forms the feature could take.
- The tradeoffs between those forms.
- Operational risks.
- Complexity and maintenance cost.
- Data-loss, duplication, or security implications.
- The recommended option and why.

The goal is to help the owner make an informed decision, not to rush into implementation.

## Approval Gate

Implementation starts only after the owner explicitly approves a direction.

Ambiguous messages such as "sounds good" can be treated as approval only if the immediately preceding message proposed a concrete implementation direction.

If the owner is still discussing tradeoffs, asking questions, or refining the PRD, do not implement.

## QA Gate

After implementation, run verification that matches the change.

Examples:

- Unit tests for parsing, mapping, redaction, and state transitions.
- Integration tests or local fakes for Redis and Tempo exporter behavior.
- Manual command output when real OpenStack, Redis, or Tempo environments are involved.
- Metrics/log validation for operational behavior.

If QA cannot be fully run locally, state exactly what was not run and why.

## Reporting Format

After implementation and QA, report:

- What changed.
- Why that approach was chosen.
- What QA was run.
- What passed or failed.
- Known limitations and follow-up decisions.

Keep the report understandable to the owner. Avoid hiding important tradeoffs inside implementation details.

## Current Planning Constraint

The project is still planning-only until the owner explicitly says planning is complete.

This workflow does not override that constraint.

