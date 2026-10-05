"""Verify CloudFormation broker roles against AWS or an explicitly chosen emulator.
No policy simulation/mocks: every positive/negative check calls SQS.
Uses only dedicated jungle-wallet-auth-validation queues; never deletes foreign messages.
Requires boto3. Never prints keys, secrets, session tokens, or message payloads.
"""
import argparse
import json
import sys
import uuid
from pathlib import Path
from urllib.parse import urlparse

import boto3
from botocore import UNSIGNED
from botocore.config import Config
from botocore.exceptions import ClientError

parser = argparse.ArgumentParser()
parser.add_argument("--outputs", required=True, help="JSON from cloudformation describe-stacks")
parser.add_argument("--profile", help="already configured AWS profile; default credential chain otherwise")
parser.add_argument("--endpoint-url", help="explicit emulator endpoint; omit for AWS")
parser.add_argument("--region", default="us-east-1")
args = parser.parse_args()
document = json.loads(Path(args.outputs).read_text())
values = {item["OutputKey"]: item["OutputValue"] for item in document["Stacks"][0]["Outputs"]}
source, events, dlq = [values[name + "Url"] for name in ("RequestQueue", "EventQueue", "DeadLetterQueue")]
for queue in (source, events, dlq):
    if not urlparse(queue).path.rsplit("/", 1)[-1].startswith("jungle-wallet-auth-validation"):
        raise SystemExit("Refusing to probe a queue outside the dedicated validation namespace")

session = boto3.Session(profile_name=args.profile, region_name=args.region)
settings = {"region_name": args.region,
            "config": Config(connect_timeout=5, read_timeout=10, retries={"max_attempts": 0})}
if args.endpoint_url:
    settings["endpoint_url"] = args.endpoint_url
sts = session.client("sts", **settings)
credentials = {}
clients = {}
for role in ("ProducerRole", "WalletRole", "EventReaderRole", "UnauthorizedRole"):
    creds = sts.assume_role(RoleArn=values[role + "Arn"], RoleSessionName="jungle-broker-auth-" + uuid.uuid4().hex[:12])["Credentials"]
    credentials[role] = creds
    clients[role] = boto3.client("sqs", aws_access_key_id=creds["AccessKeyId"],
                                aws_secret_access_key=creds["SecretAccessKey"],
                                aws_session_token=creds["SessionToken"], **settings)

marker = "broker-authorization-" + uuid.uuid4().hex
def send(client, queue):
    return client.send_message(QueueUrl=queue, MessageBody=marker,
                               MessageGroupId=marker, MessageDeduplicationId=uuid.uuid4().hex)

def positive(label, operation):
    operation()
    print("PASS authorized " + label, flush=True)

failures = []
def negative(label, operation, accepted_codes):
    try:
        operation()
    except ClientError as error:
        code = error.response["Error"]["Code"]
        if code in accepted_codes:
            print("PASS denied " + label + " (" + code + ")", flush=True)
            return
        # Connectivity/resource/configuration errors never count as an authorization denial.
        print("FAIL " + label + ": unexpected error " + code, flush=True)
        failures.append(label)
        return
    print("FAIL " + label + ": protected operation was ALLOWED", flush=True)
    failures.append(label)

def receive_and_delete_own(client, queue):
    received = client.receive_message(QueueUrl=queue, MaxNumberOfMessages=1, WaitTimeSeconds=5)
    messages = received.get("Messages", [])
    if len(messages) != 1 or messages[0].get("Body") != marker:
        raise RuntimeError("Expected this run's fixture; refusing to delete a foreign message")
    client.change_message_visibility(QueueUrl=queue, ReceiptHandle=messages[0]["ReceiptHandle"], VisibilityTimeout=30)
    client.delete_message(QueueUrl=queue, ReceiptHandle=messages[0]["ReceiptHandle"])

positive("producer SendMessage request", lambda: send(clients["ProducerRole"], source))
positive("wallet Receive/Visibility/Delete request", lambda: receive_and_delete_own(clients["WalletRole"], source))
positive("wallet SendMessage event", lambda: send(clients["WalletRole"], events))
positive("event reader Receive/Visibility/Delete event", lambda: receive_and_delete_own(clients["EventReaderRole"], events))
for queue in (source, events, dlq):
    positive("wallet GetQueueAttributes", lambda queue=queue: clients["WalletRole"].get_queue_attributes(QueueUrl=queue, AttributeNames=["QueueArn"]))
    name = urlparse(queue).path.rsplit("/", 1)[-1]
    positive("wallet GetQueueUrl", lambda name=name: clients["WalletRole"].get_queue_url(QueueName=name))

denied = ("AccessDenied", "AccessDeniedException")
negative("outsider SendMessage request", lambda: send(clients["UnauthorizedRole"], source), denied)
negative("outsider ReceiveMessage request", lambda: clients["UnauthorizedRole"].receive_message(QueueUrl=source), denied)
negative("producer ReceiveMessage request", lambda: clients["ProducerRole"].receive_message(QueueUrl=source), denied)
negative("producer SendMessage event", lambda: send(clients["ProducerRole"], events), denied)
negative("wallet SendMessage request", lambda: send(clients["WalletRole"], source), denied)
negative("event reader SendMessage event", lambda: send(clients["EventReaderRole"], events), denied)
negative("producer ReceiveMessage DLQ", lambda: clients["ProducerRole"].receive_message(QueueUrl=dlq), denied)

sender = credentials["ProducerRole"]
wrong = boto3.client("sqs", aws_access_key_id=sender["AccessKeyId"], aws_secret_access_key="wrong-secret",
                     aws_session_token=sender["SessionToken"], **settings)
negative("authorized access key with wrong secret", lambda: send(wrong, source),
         ("SignatureDoesNotMatch", "InvalidSignatureException", "AccessDenied", "AccessDeniedException"))
unknown = boto3.client("sqs", aws_access_key_id="AKIAUNKNOWN0000000000", aws_secret_access_key="wrong-secret", **settings)
negative("unknown access key", lambda: send(unknown, source),
         ("InvalidClientTokenId", "UnrecognizedClientException", "AccessDenied", "AccessDeniedException"))
unsigned_settings = dict(settings)
unsigned_settings["config"] = Config(signature_version=UNSIGNED, connect_timeout=5, read_timeout=10, retries={"max_attempts": 0})
unsigned = boto3.client("sqs", **unsigned_settings)
negative("unsigned request", lambda: send(unsigned, source),
         ("MissingAuthenticationToken", "MissingAuthenticationTokenException", "InvalidClientTokenId",
          "UnrecognizedClientException", "AccessDenied", "AccessDeniedException"))
if failures:
    print("Authorization NOT fully demonstrated: " + ", ".join(failures), flush=True)
    sys.exit(3)
print("Authorization and credential validation demonstrated by the broker.", flush=True)
