#!/bin/sh
set -eu

DLQ_URL="$(
    awslocal sqs create-queue \
        --queue-name wager-transactions-dlq.fifo \
        --attributes '{"FifoQueue":"true","ContentBasedDeduplication":"false"}' \
        --query QueueUrl \
        --output text
)"

DLQ_ARN="$(
    awslocal sqs get-queue-attributes \
        --queue-url "$DLQ_URL" \
        --attribute-names QueueArn \
        --query 'Attributes.QueueArn' \
        --output text
)"

REDRIVE_POLICY="$(printf '{"deadLetterTargetArn":"%s","maxReceiveCount":"5"}' "$DLQ_ARN")"

ATTRIBUTES="$(printf '{"FifoQueue":"true","ContentBasedDeduplication":"false","VisibilityTimeout":"30","ReceiveMessageWaitTimeSeconds":"20","RedrivePolicy":"%s"}' "$(printf '%s' "$REDRIVE_POLICY" | sed 's/"/\\"/g')")"

awslocal sqs create-queue \
    --queue-name wager-transactions.fifo \
    --attributes "$ATTRIBUTES"

awslocal sqs create-queue \
    --queue-name wager-events.fifo \
    --attributes '{"FifoQueue":"true","ContentBasedDeduplication":"false"}'