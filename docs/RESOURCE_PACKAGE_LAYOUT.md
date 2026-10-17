# Resource package layout

How the abstract and target-specific halves of a Celerity resource are split
across Go packages. This is a code-organisation convention, not a mapping
contract: for what each resource maps to on a given target, see
[contract/](./contract/).

## The convention

An abstract resource package holds the **portable** half of a resource: its
schema, its resolve, and the abstract link annotation keys an author and the
`links` package use. Each deploy target's half lives in its own package beneath
it, named `{resource}{target}`:

```
resources/topic/                  schema, resolve, abstract annotation keys
resources/topic/topicaws/         the SNS emit, property map, rewriter
resources/queue/queueaws/         the SQS queue: same on either AWS target
resources/queue/queueawsserverless/   the queue plus the topic-forwarder function
```

`{resource}aws` is for a service that is the same whichever compute model an app
runs under, and takes the deploy target as a parameter so its diagnostics name
the right one. `{resource}{target}` names a single target where the
implementation is specific to it. `handlerawsserverless` is used because a handler is
where application code lives, so it maps to the target's compute model rather
than to a service that model happens to use.

Composition happens in `transformer.go`, through
`shared.TargetImplementation`.

## Why

Go unexported names are package-scoped, so a target-specific name in an abstract
package can exist only once. `emitTopic` is not a name one target can hold while
another adds its own.

The invariant that matters is that no abstract resource package imports a target
package. The dependency points one way, target to abstract, which is what lets
a target be added without touching the portable half.

## Where a target needs more than the general package

Two resources need both halves, because `aws-serverless` adds to what
AWS-general emits:

- `queueawsserverless` calls `queueaws.Emit`, augments the queue's
  `LinkSelector` with a forward label, and adds the forwarder function.
- `sqldatabaseaws.EmitDatabase` takes an `Extender`, because anything a target
  adds has to be in the resource map before references are rewritten and tags
  applied across all of them. `sqldatabaseawsserverless` supplies the
  maintenance runner through it.

Neither `queueaws` nor `sqldatabaseaws` declares an `Implementation`:
`aws-serverless` needs more than they emit, so a constructor there would be
speculative.

## Exceptions

`consumer`, `handlerconfig` and `schedule` emit nothing themselves. Their
`EmitResult` is empty and the absorbing handler emits the concrete resource, so
they keep a per-target registration in the abstract package without carrying
target logic. Splitting them would create empty packages.

Where logic is genuinely target-agnostic, leave it unprefixed **with a comment
saying why**; prefixing it asserts something untrue. Two such cases are marked in
the code (`externalValuesConflict` in the config emit, `externalRegionSuffix` in
the resources store).
