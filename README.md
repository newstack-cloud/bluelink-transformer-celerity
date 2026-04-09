# Bluelink Transformer for Celerity

[![Coverage](https://sonarcloud.io/api/project_badges/measure?project=newstack-cloud_bluelink-transformer-celerity&metric=coverage)](https://sonarcloud.io/summary/new_code?id=newstack-cloud_bluelink-transformer-celerity)
[![Security Rating](https://sonarcloud.io/api/project_badges/measure?project=newstack-cloud_bluelink-transformer-celerity&metric=security_rating)](https://sonarcloud.io/summary/new_code?id=newstack-cloud_bluelink-transformer-celerity)
[![Maintainability Rating](https://sonarcloud.io/api/project_badges/measure?project=newstack-cloud_bluelink-transformer-celerity&metric=sqale_rating)](https://sonarcloud.io/summary/new_code?id=newstack-cloud_bluelink-transformer-celerity)

The Bluelink Transformer plugin for Celerity.
This plugin provides a collection of abstract resource types and functionality to transform Celerity resources into concrete, deployable resources on deploy targets such as AWS.

For the full list of supported abstract resources, see the [Celerity Transformer](https://registry.bluelink.dev/transformers/newstack-cloud/celerity/latest/).

For documentation on Celerity and its resource types, see the [Celerity documentation](https://www.celerityframework.io/docs/framework/applications/).

## Overview

The initial focus of the transformer (powering Celerity v0) is to support FaaS-based (serverless functions) deployments on AWS. This includes mapping Celerity resources to core AWS services like Lambda, IAM, SNS, SQS, DynamoDB, RDS and ElastiCache along with the links between them.

## Project Structure

```
resources/           # Celerity resource implementations.
  handler/           # `celerity/handler` resource implementation.
    aws/             # AWS-specific implementation of the `handler` resource.
  handlerconfig/     # `celerity/handlerConfig` resource implementation.
    aws/             # AWS-specific implementation of the `handlerConfig` resource.
  api/               # `celerity/api` resource implementation.
    aws/             # AWS-specific implementation of the `api` resource.
  consumer/          # `celerity/consumer` resource implementation.
    aws/             # AWS-specific implementation of the `consumer` resource.
  schedule/          # `celerity/schedule` resource implementation.
    aws/             # AWS-specific implementation of the `schedule` resource.
  vpc/               # `celerity/vpc` resource implementation.
    aws/             # AWS-specific implementation of the `vpc` resource.
  sqldatabase/       # `celerity/sqlDatabase` resource implementation.
    aws/             # AWS-specific implementation of the `sqldatabase` resource.
  datastore/         # `celerity/datastore` resource implementation.
    aws/             # AWS-specific implementation of the `datastore` resource.
  cache/             # `celerity/cache` resource implementation.
    aws/             # AWS-specific implementation of the `cache` resource.
  topic/             # `celerity/topic` resource implementation.
    aws/             # AWS-specific implementation of the `topic` resource.
  queue/             # `celerity/queue` resource implementation.
    aws/             # AWS-specific implementation of the `queue` resource.
  bucket/            # `celerity/bucket` resource implementation.
    aws/             # AWS-specific implementation of the `bucket` resource.
  config/            # `celerity/config` resource implementation.
    aws/             # AWS-specific implementation of the `config` resource.
utils/               # Shared utilities and helpers
internal/testutils/  # Test mocks and integration test helpers
```

## Additional Documentation

- [Contributing](./docs/CONTRIBUTING.md)
- [Commit Guidelines](./docs/COMMIT_GUIDELINES.md)
