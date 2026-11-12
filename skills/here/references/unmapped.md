If the repo should belong to a project:

- a fresh clone of a repo a mapped project lists is recognized by its `origin` remote and mapped on the spot;
- a repo with no remote needs `jig map <repo>=<path>` once, run inside the project;
- `jig ignore <path>` makes jig leave a directory alone (`--remove` undoes it).
