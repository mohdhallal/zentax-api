import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as actions from 'aws-cdk-lib/aws-cloudwatch-actions';
import * as sns from 'aws-cdk-lib/aws-sns';
import * as subscriptions from 'aws-cdk-lib/aws-sns-subscriptions';
import { Construct } from 'constructs';
import { EnvConfig } from './config';

/**
 * One SNS topic for an environment's alarms (+ the optional e-mail
 * subscription from context `alarmEmail`). Alarm actions must target a topic
 * in the alarm's own region, so the Edge stack (us-east-1, CloudFront metrics)
 * creates a second one with this same helper.
 *
 * The topic is deliberately not KMS-encrypted: CloudWatch can only publish to
 * an encrypted topic when the key policy admits cloudwatch.amazonaws.com, and
 * alarm notifications carry no customer data.
 */
export function alarmTopic(scope: Construct, cfg: EnvConfig, id: string, topicName: string): sns.Topic {
  const topic = new sns.Topic(scope, id, {
    topicName,
    displayName: `ZenTax ${cfg.name} alarms`,
    enforceSSL: true,
  });
  if (cfg.alarmEmail) {
    topic.addSubscription(new subscriptions.EmailSubscription(cfg.alarmEmail));
  }
  return topic;
}

/** Notify on ALARM and on recovery (OK). */
export function wireAlarm(alarm: cloudwatch.Alarm, topic: sns.ITopic): cloudwatch.Alarm {
  const action = new actions.SnsAction(topic);
  alarm.addAlarmAction(action);
  alarm.addOkAction(action);
  return alarm;
}

/**
 * Nominal memory of the RDS instance classes this app may be configured with.
 * Extend the table when cdk.json names a new class: the alarms need it and a
 * silent guess would make the connection/memory thresholds meaningless.
 */
const DB_CLASS_MEMORY_GIB: Record<string, number> = {
  't4g.micro': 1,
  't4g.small': 2,
  't4g.medium': 4,
  't4g.large': 8,
  't4g.xlarge': 16,
  't4g.2xlarge': 32,
  't3.micro': 1,
  't3.small': 2,
  't3.medium': 4,
  't3.large': 8,
  'm6g.large': 8,
  'm6g.xlarge': 16,
  'm6g.2xlarge': 32,
  'm7g.large': 8,
  'm7g.xlarge': 16,
  'm7g.2xlarge': 32,
  'r6g.large': 16,
  'r6g.xlarge': 32,
  'r7g.large': 16,
  'r7g.xlarge': 32,
};

/**
 * RDS PostgreSQL's default `max_connections` is
 * `LEAST({DBInstanceClassMemory/9531392}, 5000)`. DBInstanceClassMemory is a
 * little below the nominal RAM (the OS keeps some), so the value here is an
 * upper bound; the 80 % alarm threshold absorbs the difference. Assumption is
 * stated in the alarm description.
 */
export function postgresMaxConnections(instanceClass: string): { maxConnections: number; memoryBytes: number } {
  const gib = DB_CLASS_MEMORY_GIB[instanceClass];
  if (gib === undefined) {
    throw new Error(`alarms: unknown RDS instance class "${instanceClass}" — add its memory to DB_CLASS_MEMORY_GIB in lib/alarms.ts`);
  }
  const memoryBytes = gib * 1024 ** 3;
  return { maxConnections: Math.min(Math.floor(memoryBytes / 9531392), 5000), memoryBytes };
}
