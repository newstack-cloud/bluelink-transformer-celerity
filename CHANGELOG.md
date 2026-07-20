# Changelog

All notable changes to this project will be documented in this file.

## [0.1.1] - 2026-07-20

### Bug Fixes

- Add fix to self-referential schema to prevent infinite recursion in docgen([aa3fab6](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/aa3fab6c365af903b86fdf43a7711998229f1a5e))
## [0.1.0] - 2026-07-20

### Bug Fixes

- **lint:** Resolve golangci-lint failures([7fbba02](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/7fbba02b46ee5e99b3970cc3aa1b8c1432a56e34))
- **api:** Stop emission when no protocol is declared([672a5d2](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/672a5d25c3a010c56e379a005c150d16df15d6e0))
- **api:** Don't emit invalid authorizers on WebSocket-only APIs([b1e031a](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/b1e031a62b21b09c513b14bbe73a3cd8aad0ac26))
- **api:** Error on colliding hybrid custom-domain base paths([6850acc](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/6850accd852f1e753cf3100adb73bc798a2f1ebb))
- **api:** Error on jwt guards missing required fields([8387aaf](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/8387aaf49712ddaa8d3a4b7252fd2e545342086d))
- **cache:** Protect auth selector label from user override([f5737ba](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/f5737ba9aa252437218c81663e463f9da945de0b))
- **datastore:** Set GSI throughput under PROVISIONED billing([8814ddc](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/8814ddcb2674e2b7c150d06cfff9f07d0edcdc6d))
- **datastore:** Enforce GSI field cardinality of one or two([feeeba8](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/feeeba80c638eef14d41819b92b130c7d3d8376f))
- **datastore:** Require timeToLive.fieldName when the block is set([fee36e0](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/fee36e054e34ea1ecacbfc940a8f9be33482b292))
- **handler:** Keep S3 trigger event indices unique across consumers([900ab3f](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/900ab3f1b5463364636141f313f56a5f8078349b))
- **handler:** Normalise external SQS URL sourceId to ARN for event source([0898d30](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/0898d30e7d950eeb93c04b4b8b367e6d070a0494))
- **sqldatabase:** Reference emitted subnet group and proxy for dep edges([feefe73](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/feefe736187058519eedd37e47baab60d1cf582d))
- **sqldatabase:** Set proxy defaultAuthScheme IAM_AUTH in iam mode([a8217c9](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/a8217c970814907935e400b7be143a8ab6c34343))
- **iam:** Grant stream ListStreams on "*" not the source ARN([9968181](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/9968181418b38e5a8a286ba634e3fef327344ab8))
- Deterministic tags, cors/tracing schema accuracy, stronger tests([98171ac](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/98171ac0d89b4b9743b14973e76f7dd81fc2044f))
- Dependency edges, encryption/guard/VPC invariants, IAM actions([3bb3884](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/3bb38840f023f028ecb3f33e6b979aa8b0b07cea))
- **handler:** Warn (not fail) on missing manifest; flag consumer label conflicts([9aeb437](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/9aeb437ec357d599ed03804754b4818f708507b3))
- **sqldatabase:** Grant proxy role rds-db:connect in iam mode([290a728](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/290a728016d760ffc4e0977c5c68e9b3dcf75bf5))
- **handler:** No WS gateway authorizer; error on empty schedule expression([1bbf677](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/1bbf6778a97495edbd40cc84383886925a53f554))
- **links:** Address M1 review — DLQ cardinality side + plain-text/diagnostic polish([7966e5e](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/7966e5e67a72bf0f0c1725efa196b2af980e32b6))
- **handler:** Wire RDS iam authMode annotation for handler->sqlDatabase links([2c058ea](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/2c058eaea585ca94904cbb75d109ca683d590152))
- **api,handler:** Scope hybrid API linkSelectors per protocol([39b59cd](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/39b59cd64749b990c3fd4cfe6e6d76201ca715d4))
- **queue:** Rework topic forwarder for correct fan-out (M2 review)([335d487](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/335d4874064d5db295cf74d4f01d8cc49f37a9f9))
- **transformer:** Make resources store deterministic + infallible (M3 review)([3499251](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/3499251be5918609a0b08b38f7abc704937c2e1c))
- **transformer:** Degrade unloadable build manifest to fallback, not fatal([b3d01dd](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/b3d01dd2b4bf34abda04d1e129001f7a4e89b2bb))
- Avoid re-publishing to succeeded topics and preserve build-manifest load cause([9fd0cbd](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/9fd0cbdfdf3690cfe958919ab9e431baa727db47))

### Features

- Add initial handler resource aws sls transform([d61a19f](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/d61a19fa50143dc83b0b34226aa7c9d22ad240ff))
- Add initial handler config aws sls transform([0766bfd](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/0766bfdd242feaebab1f269ab0872c451228b48d))
- Add initial implementation of config aws sls transform([446d593](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/446d593638f5de3c89938b537fac30e3ae2ec6cc))
- Add shared helpers and constants used throughout the plugin([0b30502](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/0b30502fde5b59751cbe09354f4c843491988c51))
- Wire up transformer with initial transforms and stubs([d7e290f](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/d7e290f76e6c842331cc96ded1fd197f7f7e5e73))
- Add handler and handler config abstract links([c738776](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/c738776e32be226a2670d4b4eb91a640077561f7))
- Add type definition for linked resources([903b899](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/903b8995334f85f18a6c170b478cf887969e70c9))
- Add queue aws transform([d4421aa](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/d4421aa0cafa56fed71b1a793037d37832aa0d96))
- **shared:** Aws deploy-config resolver, tag/metadata helpers, external-source IAM([d344644](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/d344644964440ee30832e88618a4f1b9396bf798))
- Implement remaining v0 aws-serverless resource slices([7aadc42](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/7aadc42524fb06c2387f3ae85714ab49815855cd))
- **cache:** Implement authMode iam via ElastiCache RBAC([1ff0bda](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/1ff0bda3abce6946020aac3f45b9555f5cb48d21))
- **handler:** Warn on conflicting per-function consumer triggers([c0753e8](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/c0753e85ada04e93eba1f1c2f62d4446e3f41a4b))
- **links:** Complete the 12 empty abstract link definitions + invariant guard([bc3ec02](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/bc3ec0299e393f6aab25d9f537103c9f31ef8d16))
- **queue,topic:** Emit S3 notification event/filter config for bucket links([9e5268d](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/9e5268d70d7536e5294456b807f525fc4d8c6995))
- **queue:** Emit intermediary forwarder for queue->topic message forwarding([fa5904b](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/fa5904b5ffa83a4d7af1b629af7ec5afe6364faf))
- **transformer:** Emit internal resources config store for runtime resolution([478bc5d](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/478bc5dc269c6dbdb0452578a4456f3ded378927))
- Add initial complete implementation for celerity v0 aws sls([6028172](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/60281722430fa50f0d1529141b406e81aad3fa99))

### Refactoring

- Trim comments to load-bearing context([7cde6ff](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/7cde6ffb0ef09fedbdbc8ca433769ce71a7b78b0))
- **cache:** Extract emitCache helpers to reduce cognitive complexity([f39381f](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/f39381f1a138a8b1dbe69d8bcf766ae8d7a4de6a))
- **sqldatabase:** Extract presetSuitabilityError to reduce cognitive complexity([c89b1a2](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/c89b1a20333d076c6f16788f36d65171e6426d13))
- Code-health pass — maps.Copy for map copies + reduce function size([80dd52f](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/80dd52fcdb1932f49bf1cc31fa848d8e604370f1))

### Testing

- Add support for integration tests for sourcing handler build manifests([91e87c9](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/91e87c9773fe1f96998fcb1b1f1514e3694c1083))
- Unit suites for all v0 aws-serverless slices([6a4d0e1](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/6a4d0e1f887e3871aa0f5fcc4f93ed585bbbbf32))
- Strengthen datastore attribute-type and topic DLQ coverage([55b2242](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/55b22420b800925a1c940b02b46488f096dd44b0))
- **api:** Complete jwt guard fixtures in protocol-filtering tests([b52763e](https://github.com/newstack-cloud/bluelink-transformer-celerity/commit/b52763e4d3873c02264ff17344038a02d30f618b))

