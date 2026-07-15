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
transformer/
  transformer.go     # Transformer plugin definition and blueprint transform function.
resources/           # Celerity resource implementations.
  handler/           # `celerity/handler` resource implementation.
    handler.go
    handler_aws.go
  handlerconfig/     # `celerity/handlerConfig` resource implementation.
    handlerconfig.go
    handlerconfig_aws.go
  api/               # `celerity/api` resource implementation.
    api.go
    api_aws.go
  consumer/          # `celerity/consumer` resource implementation.
    consumer.go
    consumer_aws.go
  schedule/          # `celerity/schedule` resource implementation.
    schedule.go
    schedule_aws.go
  vpc/               # `celerity/vpc` resource implementation.
    vpc.go
    vpc_aws.go
  sqldatabase/       # `celerity/sqlDatabase` resource implementation.
    sqldatabase.go
    sqldatabase_aws.go
  datastore/         # `celerity/datastore` resource implementation.
    datastore.go
    datastore_aws.go
  cache/             # `celerity/cache` resource implementation.
    cache.go
    cache_aws.go
  topic/             # `celerity/topic` resource implementation.
    topic.go
    topic_aws.go
  queue/             # `celerity/queue` resource implementation.
    queue.go
    queue_aws.go
  bucket/            # `celerity/bucket` resource implementation.
    bucket.go
    bucket_aws.go
  config/            # `celerity/config` resource implementation.
    config.go
    config_aws.go
version.go           # Version information set via ldflags at build time.
```

## Additional Documentation

- [Contributing](./docs/CONTRIBUTING.md)
- [Commit Guidelines](./docs/COMMIT_GUIDELINES.md)
