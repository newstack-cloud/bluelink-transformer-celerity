# Commit Guidelines

## 1) Conventional Commits

For all contributions to this repo, you must use the conventional commits standard defined [here](https://www.conventionalcommits.org/en/v1.0.0/).

This is used to generate automated change logs, allow for tooling to decide semantic versions for packages and applications,
provide a rich and meaningful commit history along with providing
a base for more advanced tooling to allow for efficient searches for decisions and context related to commits and code.

### Commit types

**The following commit types are supported in the Bluelink Transformer for Celerity:**

- `fix:` - Should be used for any bug fixes.
- `build:` - Should be used for functionality related to building the application.
- `revert:` - Should be used for any commits that revert changes.
- `wip:` - Should be used for commits that contain work in progress.
- `feat:` - Should be used for any new features added, regardless of the size of the feature.
- `chore:` - Should be used for tasks such as releases or patching dependencies.
- `ci:` - Should be used for any work on GitHub Action workflows or scripts used in CI.
- `docs:`- Should be used for adding or modifying documentation.
- `style:` - Should be used for code formatting commits and linting fixes.
- `refactor:` - Should be used for any type of refactoring work that is not a part of a feature or bug fix.
- `perf:` - Should be used for a commit that represents performance improvements.
- `test:` - Should be used for commits that are purely for automated tests.
- `instr:` - Should be used for commit that are for instrumentation purposes. (e.g. logs, trace spans and telemetry configuration)

**`fix` and `feat` commit types must be used for changes that will impact the next release version.**

### Commit footers

**The following custom footers are supported:**

- `GitHubIssue: #xxx` - This footer must be provided when a commit pertains to some work where there is a GitHub issue. 
  This helps with tooling that links GitHub issues to commits providing a way to easily get extra context and requirements
  that are related to a commit. You can also use the `#xxx` reference in the body of the message to reference GitHub issues.

### Example commit

#### With commit scope

```bash
git commit -m 'feat: update google cloud implementation for celerity handler resource

Adds new functionality to the Google Cloud implementation of the Celerity `handler` resource to support new features and configurations.

GitHubIssue: #2391
'
```

#### Without commit scope

```bash
git commit -m 'fix: correct the implementation of the AWS provider for the Celerity schedule resource'
```

## 2) You must use the imperative mood for commit headers.

https://cbea.ms/git-commit/#imperative

The imperative mood simply means naming the subject of the commit as if it is a unit of work that can be applied instead of reporting facts about work done.

If applied, this commit will **your subject line here**.

Read the article above to find more examples and tips for using the imperative mood.
