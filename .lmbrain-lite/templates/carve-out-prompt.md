# Carve out an isolated feature

Call `lite_digest` first. From the work in progress, carve out one small feature that another agent could build in isolation, with no access to this session: narrow surface, few dependencies on what is currently in flight, and a result that drops back into the branch.

Then request a sub-agent for it with `lite_subagent_request`, following `templates/subagent-brief.md`: the task it serves, a self-contained brief (what to build, what is already decided, what not to touch), the files it may touch, how it can tell it is done, and the tier that names the kind of work. Do not build it yourself and do not change the roadmap. I will dispatch or decline the request in Sessions; read the outcome back with `lite_subagent_list`. When the work comes back, you own the review against your brief and its exit criterion.
