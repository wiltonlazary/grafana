---
aliases:
  - /docs/grafana/latest/auth/github/
  - /docs/grafana/latest/setup-grafana/configure-security/configure-authentication/github/
description: Grafana OAuthentication Guide
keywords:
  - grafana
  - configuration
  - documentation
  - oauth
title: Configure GitHub OAuth2 Authentication
weight: 1400
---

# Configure GitHub OAuth2 authentication

To enable the GitHub OAuth2 you must register your application with GitHub. GitHub will generate a client ID and secret key for you to use.

## Configure GitHub OAuth application

You need to create a GitHub OAuth application (you will find this under the GitHub
settings page). When you create the application you will need to specify
a callback URL. Specify this as callback:

```bash
http://<my_grafana_server_name_or_ip>:<grafana_server_port>/grafana/login/github
```

This callback URL must match the full HTTP address that you use in your
browser to access Grafana, but with the suffix path of `/login/github`.
When the GitHub OAuth application is created you will get a Client ID and a
Client Secret. Specify these in the Grafana configuration file. For
example:

## Enable GitHub in Grafana

```bash
[auth.github]
enabled = true
allow_sign_up = true
client_id = YOUR_GITHUB_APP_CLIENT_ID
client_secret = YOUR_GITHUB_APP_CLIENT_SECRET
scopes = user:email,read:org
auth_url = https://github.com/login/oauth/authorize
token_url = https://github.com/login/oauth/access_token
api_url = https://api.github.com/user
team_ids =
allowed_organizations =
```

You may have to set the `root_url` option of `[server]` for the callback URL to be
correct. For example in case you are serving Grafana behind a proxy.

Restart the Grafana back-end. You should now see a GitHub login button
on the login page. You can now login or sign up with your GitHub
accounts.

You may allow users to sign-up via GitHub authentication by setting the
`allow_sign_up` option to `true`. When this option is set to `true`, any
user successfully authenticating via GitHub authentication will be
automatically signed up.

### team_ids

Require an active team membership for at least one of the given teams on
GitHub. If the authenticated user isn't a member of at least one of the
teams they will not be able to register or authenticate with your
Grafana instance. For example:

```bash
[auth.github]
enabled = true
client_id = YOUR_GITHUB_APP_CLIENT_ID
client_secret = YOUR_GITHUB_APP_CLIENT_SECRET
scopes = user:email,read:org
team_ids = 150,300
auth_url = https://github.com/login/oauth/authorize
token_url = https://github.com/login/oauth/access_token
api_url = https://api.github.com/user
allow_sign_up = true
```

### allowed_organizations

Require an active organization membership for at least one of the given
organizations on GitHub. If the authenticated user isn't a member of at least
one of the organizations they will not be able to register or authenticate with
your Grafana instance. For example

```bash
[auth.github]
enabled = true
client_id = YOUR_GITHUB_APP_CLIENT_ID
client_secret = YOUR_GITHUB_APP_CLIENT_SECRET
scopes = user:email,read:org
auth_url = https://github.com/login/oauth/authorize
token_url = https://github.com/login/oauth/access_token
api_url = https://api.github.com/user
allow_sign_up = true
# space-delimited organization names
allowed_organizations = github google
```

### Map roles

You can use GitHub OAuth to map roles. During mapping, Grafana checks for the presence of a role using the [JMESPath](http://jmespath.org/examples.html) specified via the `role_attribute_path` configuration option.

For the path lookup, Grafana uses JSON obtained from querying GitHub's API [`/api/user`](https://docs.github.com/en/rest/users/users#get-the-authenticated-user=) endpoint and a `groups` key containing all of the user's teams (retrieved from `/api/user/teams`).

The result of evaluating the `role_attribute_path` JMESPath expression must be a valid Grafana role, for example, `Viewer`, `Editor` or `Admin`. For more information about roles and permissions in Grafana, refer to [Roles and permissions]({{< relref "../../../administration/roles-and-permissions/" >}}).

An example Query could look like the following:

```bash
role_attribute_path = [login==octocat] && 'Admin' || 'Viewer'
```

This allows the user with login "octocat" to be mapped to the `Admin` role,
but all other users to be mapped to the `Viewer` role.

#### Map roles using teams

Teams can also be used to map roles. For instance,
if you have a team called 'example-group' you can use the following snippet to
ensure those members inherit the role 'Editor'.

```bash
role_attribute_path = contains(groups[*], '@github/example-group') && 'Editor' || 'Viewer'
```

Note: If a match is found in other fields, teams will be ignored.

### Team Sync (Enterprise only)

> Only available in Grafana Enterprise v6.3+

With Team Sync you can map your GitHub org teams to teams in Grafana so that your users will automatically be added to
the correct teams.

Your GitHub teams can be referenced in two ways:

- `https://github.com/orgs/<org>/teams/<slug>`
- `@<org>/<slug>`

Example: `@grafana/developers`

[Learn more about Team Sync]({{< relref "../configure-team-sync/" >}})
