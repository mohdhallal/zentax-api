import * as iam from 'aws-cdk-lib/aws-iam';
import { Construct } from 'constructs';
import { EnvConfig } from './config';

/**
 * Task + execution roles with pinned names (`zentax-<env>-<component>-task|exec`):
 * the GitHub deploy roles' `iam:PassRole` is scoped to `role/zentax-<env>-*`.
 * CDK attaches the least-privilege inline policies (pull image, read the
 * referenced secrets, write to the referenced log group) as they are used.
 */
export function makeExecutionRole(scope: Construct, cfg: EnvConfig, component: string): iam.Role {
  return new iam.Role(scope, `${component}ExecRole`, {
    roleName: `zentax-${cfg.name}-${component}-exec`,
    assumedBy: new iam.ServicePrincipal('ecs-tasks.amazonaws.com'),
    description: `ZenTax ${cfg.name} ${component}: ECS agent (pull image, read secrets, write logs)`,
  });
}

export function makeTaskRole(scope: Construct, cfg: EnvConfig, component: string): iam.Role {
  return new iam.Role(scope, `${component}TaskRole`, {
    roleName: `zentax-${cfg.name}-${component}-task`,
    assumedBy: new iam.ServicePrincipal('ecs-tasks.amazonaws.com'),
    description: `ZenTax ${cfg.name} ${component}: what the running container may do`,
  });
}
