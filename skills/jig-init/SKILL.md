---
name: jig-init
description: Starts a jig project in a folder that has none - one repo, or several nested repos - with a ticket store beside it. Use when the person asks to set up jig for a repo or folder and `jig here` prints nothing.
---

# jig-init

Start a project only when the person asked for one. `jig here` printing nothing means the folder is in no project; that alone is not a reason to run `jig init`. If `jig here` names a project, stop and use `jig-attach`.

## Pick the shape

| The folder is | Run |
|---|---|
| one git repo that is the whole project | `jig init --standalone` |
| a folder holding several repos (or you have their urls) | `jig init --orchestrator <path or url>...` from that folder |
| a repo of a project somebody else started | not this skill: `jig-attach` |

When the folder does not say which, look for `.git` directories under it and ask the person.

## Steps

1. `jig here` - confirm there is no project.
2. Run the `jig init` line above. Add `--key <KEY>` to choose the ticket id prefix, `--clone` to clone the urls you gave, `--yes` once the person has agreed to what it will create. It writes a `jig.yaml` marker in the folder and creates the ticket store; read what it prints.
3. `jig here` again: it names the project, its store and the repo.
4. `jig attach` - sets this machine up for the project (skills, hooks, git hooks). `jig-attach` explains it.
5. When the person has work in mind, `jig ticket new --title "..."`; the `intake` skill drafts its brief.

## Keeping the project in step

```
jig repos                     # the project's repos
jig repos add <path|url>      # add one (--clone to clone a url)
jig repos remove <name>
jig repos sync                # bring the marker and this machine into agreement (--yes to apply)
jig share --store-remote <url> --create --yes   # publish the store so others can clone it
jig share --remote <name>=<url>                 # record a repo's remote
```

A fresh clone of a repo that has the marker needs no init: `jig here` recognizes it.

## Never

- Do not write or edit `jig.yaml` by hand; `init`, `repos` and `share` own it.
- Do not run `init` where `jig here` already finds a project.
- Do not create remotes or push the store unless the person said to.
