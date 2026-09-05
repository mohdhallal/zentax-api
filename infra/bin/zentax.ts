#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { configuredEnvNames, isEnvName } from '../lib/config';
import { addDns, addEnvironment, addGithubOidc } from '../lib/zentax-app';

const app = new cdk.App();

// Always present (account-level, admin-deployed): the GitHub OIDC provider +
// deploy roles, and the apex hosted zone.
addGithubOidc(app);
addDns(app);

// `--context env=<cell>` (staging-eu | production-eu, whatever cdk.json
// declares) selects one cell; omit it to list/synth all.
const requested = app.node.tryGetContext('env');
if (requested === undefined) {
  for (const name of configuredEnvNames(app)) addEnvironment(app, name);
} else if (isEnvName(app, requested)) {
  addEnvironment(app, requested);
} else {
  throw new Error(`--context env=${String(requested)}: expected one of ${configuredEnvNames(app).join(', ')} (cdk.json context.environments)`);
}

app.synth();
