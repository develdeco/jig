# A start-to-finish run and going with recommendations are separate

[ADR 0033](0033-phases-are-asked-for-by-prompt.md) let a prompt name several phases for an automatic run (decision 37) and let it hand over the person's two moments (decision 38): "go with your recommendations" handed over the brief's approval, and a prompt that ended at the pull request, or said "and publish it", handed over the publish approval. The README's recording read "Take SHOP-1 from brief to PR ... go with your recommendations, then build it, review it and publish it", so the one run showed both at once, and a prompt that only named the phases already gave the publish away.

The owner's rule (2026-10-27): running from start to finish means the main session goes on to each next phase without waiting for a prompt to start it; it does not mean the session decides for the person. The questions are still asked, and in the recording it looks like the person answering them. Going with recommendations is a separate thing the prompt has to say. A run can be start-to-finish, go with recommendations, or both, and only both runs with nobody to ask.

## Decision

**A prompt that names several phases is a start-to-finish run, and hands over no decision.** The session goes on from each phase to the next with its skill and ends the run at a refusal, as decision 37 has it. The brief's questions and its approval, the asks a review leaves and the publish approval stay the person's: the session asks each in the session and waits for the answer. A start-to-finish run that names the publish asks the person to approve it and, on their yes, publishes the hand-off way (its own push, then `jig publish --pr`). "... to PR" and "and publish it" name the publish as a phase of the run; they no longer hand over its approval.

**Going with recommendations is a hand-over the prompt says.** A prompt that tells the session to go with its recommendations, in one phase or a run, lets it answer those decisions itself: the grilling round with its recommended answers, each tagged `defaulted`; the brief's approval, recorded with agent provenance as `jig validate` off a terminal already records it; a review's asks, decided with `jig triage`; the publish, the hand-off way, which asks for no approval. jig's own push still takes a person's approval, and an agent's `jig publish --approve` never stands for one there.

**It is skill text and needs no code.** The project's skill stub and the here, intake, build, review and publish skills say it. Whether a prompt names phases or hands over decisions is the session's reading of the person's words; jig records the decisions with their provenance, as before.

## Consequences

- A person who wants a whole ticket done without stopping says both: "take it from brief to PR, and go with your recommendations". One who wants to answer the questions and approve, but not to type a prompt per phase, says only the first.
- The README's recording asks without recommendations, so the session's questions and the approvals show the person answering them.
- Supersedes the hand-over of the publish in [ADR 0033](0033-phases-are-asked-for-by-prompt.md)'s decision 38.
