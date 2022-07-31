# OAUTH BLOCK
## Devenv setup jwt auth

To launch the block, use the oauth source. Ex:

```bash
make devenv sources="jwt_proxy"
```

Here is the conf you need to add to your configuration file (conf/custom.ini):

```ini
[auth]
signout_redirect_url = http://127.0.0.1:8088/oauth2/sign_out

[auth.jwt]
enabled = true
enable_login_token = true
header_name = X-Forwarded-Access-Token
username_claim = login
email_claim = email
jwk_set_file = devenv/docker/blocks/oauth/jwks.json
cache_ttl = 60m
expected_claims = {"iss": "http://localhost:8087/auth/realms/grafana", "azp": "grafana-oauth"}
auto_sign_up = true
```

Access Grafana through: 

```sh
http://127.0.0.1:8088
```

## Devenv setup jwt auth iframe embedding

- Add previous configuration and next snippet to grafana.ini

```ini
[security]
allow_embedding = true
```

- Create dashboard and copy UID

- Clone [https://github.com/grafana/grafana-iframe-oauth-sample](https://github.com/grafana/grafana-iframe-oauth-sample)

- Change the dashboard URL in `grafana-iframe-oauth-sample/src/pages/restricted.tsx` to use the dashboard you created (keep URL query values)

- Start sample app from the `grafana-iframe-oauth-sample` folder with: `yarn start`

- Navigate to [http://localhost:4200](http://localhost:4200) and press restricted area

Note: You may need to grant the JWT user in grafana access to the datasources and the dashboard

## Backing up keycloak DB

In case you want to make changes to the devenv setup, you can dump keycloack's DB:

```bash
cd devenv;
docker-compose exec -T oauthkeycloakdb bash -c "pg_dump -U keycloak keycloak" > docker/blocks/jwt_proxy/cloak.sql
```

## Connecting to keycloack:

- keycloak admin:                     http://localhost:8087
- keycloak admin login:               admin:admin
- grafana jwt viewer login:          jwt-viewer:grafana
- grafana jwt editor login:          jwt-editor:grafana
- grafana jwt admin login:           jwt-admin:grafana

# Troubleshooting

## Mac M1 Users

The new arm64 architecture does not build for the latest docker image of keycloack. Refer to https://github.com/docker/for-mac/issues/5310 for the issue to see if it resolved.
Until then you need to build the docker image locally and then run `devenv`.

1. Remove any lingering keycloack image
```sh
$ docker rmi $(docker images | grep 'keycloack')
```
1. Build keycloack image locally
```sh
$ ./docker-build-keycloack-m1-image.sh
```
1. Start from beginning of this readme
