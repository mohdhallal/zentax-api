#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { isEnvName } from '../lib/config';
import { addEnvironment, addGithubOidc } from '../lib/zentax-app';

const app = new cdk.App();

// Always present (account-level): the GitHub OIDC provider + deploy roles.
addGithubOidc(app);

// `--context env=staging|production` selects one cell; omit it to list/synth all.
const requested = app.node.tryGetContext('env');
if (requested === undefined) {
  addEnvironment(app, 'staging');
  addEnvironment(app, 'production');
} else if (isEnvName(requested)) {
  addEnvironment(app, requested);
} else {
  throw new Error(`--context env=${String(requested)}: expected "staging" or "production"`);
}

app.synth();
