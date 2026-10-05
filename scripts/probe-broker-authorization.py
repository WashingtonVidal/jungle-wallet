"""Capability probe for a separate local emulator; never targets application queues.
Requires boto3. Keeps its uniquely named resources for inspection; no deletion.
Exit 0: enforcement demonstrated. Exit 3: one or more protected calls were allowed.
"""
import json
import sys
import time
import uuid

import boto3
from botocore.config import Config
from botocore.exceptions import ClientError

endpoint = "http://localhost:4566"
settings = dict(endpoint_url=endpoint, region_name="us-east-1",
                config=Config(connect_timeout=2, read_timeout=5, retries={"max_attempts": 0}))
admin = boto3.client("iam", aws_access_key_id="test", aws_secret_access_key="test", **settings)
broker = boto3.client("sqs", aws_access_key_id="test", aws_secret_access_key="test", **settings)
for attempt in range(30):
    try:
        admin.list_users()
        break
    except Exception:
        if attempt == 29:
            raise
        time.sleep(0.5)

suffix = uuid.uuid4().hex[:12]
queue = broker.create_queue(QueueName="broker-auth-probe-" + suffix + ".fifo",
                           Attributes={"FifoQueue": "true", "ContentBasedDeduplication": "false"})["QueueUrl"]
arn = broker.get_queue_attributes(QueueUrl=queue, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"]
clients = {}
sender_key = None
for identity in ("sender", "outsider"):
    user = "broker-auth-probe-" + identity + "-" + suffix
    user_arn = admin.create_user(UserName=user)["User"]["Arn"]
    if identity == "sender":
        admin.put_user_policy(UserName=user, PolicyName="SendOnly",
                              PolicyDocument=json.dumps({"Version": "2012-10-17", "Statement": [
                                  {"Effect": "Allow", "Action": "sqs:SendMessage", "Resource": arn}]}))
    key = admin.create_access_key(UserName=user)["AccessKey"]
    if identity == "sender":
        sender_key = key
    clients[identity] = boto3.client("sqs", aws_access_key_id=key["AccessKeyId"],
                                    aws_secret_access_key=key["SecretAccessKey"], **settings)
    if identity == "outsider":
        broker.set_queue_attributes(QueueUrl=queue, Attributes={"Policy": json.dumps({
            "Version": "2012-10-17", "Statement": [{"Effect": "Deny", "Principal": "*",
            "Action": "sqs:*", "Resource": arn,
            "Condition": {"ArnEquals": {"aws:PrincipalArn": user_arn}}}]})})

def send(client):
    return client.send_message(QueueUrl=queue, MessageBody="authorization probe only",
                               MessageGroupId="probe", MessageDeduplicationId=uuid.uuid4().hex)

send(clients["sender"])
print("authorized sender SendMessage: ALLOWED", flush=True)
failures = 0
for label, operation in [
    ("outsider SendMessage (no grant and explicit queue Deny)", lambda: send(clients["outsider"])),
    ("sender ReceiveMessage (outside SendOnly policy)", lambda: clients["sender"].receive_message(QueueUrl=queue)),
    ("sender key with wrong secret", lambda: send(boto3.client("sqs", aws_access_key_id=sender_key["AccessKeyId"], aws_secret_access_key="wrong-secret", **settings))),
    ("unknown access key", lambda: send(boto3.client("sqs", aws_access_key_id="AKIAUNKNOWN0000000000", aws_secret_access_key="wrong-secret", **settings))),
]:
    try:
        operation()
        print(label + ": ALLOWED -- enforcement NOT demonstrated", flush=True)
        failures += 1
    except ClientError as error:
        code = error.response["Error"]["Code"]
        if code not in ("AccessDenied", "AccessDeniedException", "SignatureDoesNotMatch", "InvalidClientTokenId", "UnrecognizedClientException"):
            raise
        print(label + ": DENIED (" + code + ")", flush=True)
print("probe queue retained: " + arn, flush=True)
sys.exit(3 if failures else 0)
