# Discord OAuth2 & Bot Authentication

Reference for Discord's authentication mechanisms. Based on the
[Discord Developer Documentation](https://discord.com/developers/docs/topics/oauth2).

## Endpoints

| Purpose         | URL                                            |
|-----------------|------------------------------------------------|
| Authorize       | `https://discord.com/oauth2/authorize`         |
| Token Exchange  | `https://discord.com/api/oauth2/token`         |
| Token Revoke    | `https://discord.com/api/oauth2/token/revoke`  |
| Current Auth    | `GET https://discord.com/api/oauth2/@me`       |
| API Base (v10)  | `https://discord.com/api/v10`                  |
| Gateway         | `GET https://discord.com/api/v10/gateway`      |
| Gateway (Bot)   | `GET https://discord.com/api/v10/gateway/bot`  |

## Bot Token vs Bearer Token

| Property      | Bot Token                          | Bearer Token                       |
|---------------|------------------------------------|------------------------------------|
| Header        | `Authorization: Bot <token>`       | `Authorization: Bearer <token>`    |
| Source        | Developer Portal (static)          | OAuth2 flow (dynamic)              |
| Expires       | Never (until reset)                | 604800s (7 days) default           |
| Refresh       | N/A                                | Via `refresh_token` grant          |
| Gateway       | Yes                                | No                                 |
| Scoped        | No (full bot access)               | Yes (limited to granted scopes)    |
| Represents    | Bot application                    | A user's delegated authorization   |

## OAuth2 Authorization Code Grant

This is the standard flow for obtaining a Bearer token on behalf of a user.

### Step 1 — Build Authorization URL

```
https://discord.com/oauth2/authorize
  ?response_type=code
  &client_id={CLIENT_ID}
  &scope=identify%20guilds
  &state={UNIQUE_STATE}
  &redirect_uri={ENCODED_REDIRECT_URI}
  &prompt=consent
```

Parameters:
- `response_type=code` — required
- `client_id` — your application ID
- `scope` — space-separated, URL-encoded with `%20`
- `redirect_uri` — must match a registered redirect URI
- `state` — CSRF protection, cryptographically random
- `prompt` — `consent` forces re-auth, `none` skips if already authorized
- `integration_type` — `0` for GUILD_INSTALL, `1` for USER_INSTALL

### Step 2 — User Authorizes, Discord Redirects

```
https://yoursite.com/callback?code={AUTHORIZATION_CODE}&state={UNIQUE_STATE}
```

### Step 3 — Exchange Code for Token

```http
POST https://discord.com/api/oauth2/token
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code
&code={CODE}
&redirect_uri={REDIRECT_URI}
&client_id={CLIENT_ID}
&client_secret={CLIENT_SECRET}
```

**Important:** Token endpoints require `application/x-www-form-urlencoded`, not JSON.

Response:
```json
{
  "access_token": "...",
  "token_type": "Bearer",
  "expires_in": 604800,
  "refresh_token": "...",
  "scope": "identify guilds"
}
```

### Step 4 — Refresh Token

```http
POST https://discord.com/api/oauth2/token
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token
&refresh_token={REFRESH_TOKEN}
&client_id={CLIENT_ID}
&client_secret={CLIENT_SECRET}
```

### Step 5 — Revoke Token

```http
POST https://discord.com/api/oauth2/token/revoke
Content-Type: application/x-www-form-urlencoded

token={TOKEN}
&token_type_hint=access_token
```

Revoking any token revokes ALL tokens for that authorization.

## Bot Authorization Flow

Used to add a bot to a guild. No `response_type` or `redirect_uri` needed:

```
https://discord.com/oauth2/authorize
  ?client_id={CLIENT_ID}
  &scope=bot%20applications.commands
  &permissions={PERMISSION_INTEGER}
```

Optional parameters:
- `guild_id` — pre-select a guild
- `disable_guild_select` — lock guild selection

If additional scopes are included beyond `bot` (e.g. `bot identify`), it becomes
a full authorization code grant and returns a bearer token plus bot data:

```json
{
  "token_type": "Bearer",
  "access_token": "...",
  "refresh_token": "...",
  "expires_in": 604800,
  "scope": "bot identify",
  "guild": { "..." },
  "permissions": "..."
}
```

## Client Credentials Grant

For obtaining a bearer token without user interaction:

```http
POST https://discord.com/api/oauth2/token
Content-Type: application/x-www-form-urlencoded
Authorization: Basic base64(client_id:client_secret)

grant_type=client_credentials
&scope=identify%20connections
```

## Scopes

### Identity & User Data

| Scope | Description |
|-------|-------------|
| `identify` | Read user info (id, username, avatar) without email |
| `email` | Includes email in user identity |
| `connections` | Read user's linked third-party accounts |

### Guild & Member

| Scope | Description |
|-------|-------------|
| `guilds` | Read user's guild list |
| `guilds.join` | Add user to a guild (requires bot in that guild) |
| `guilds.members.read` | Read member data in user's guilds |

### Bot & Application

| Scope | Description |
|-------|-------------|
| `bot` | Add bot user to a guild |
| `applications.commands` | Add slash commands to a guild |
| `applications.commands.update` | Modify application commands via bearer token |
| `applications.commands.permissions.update` | Modify command permissions |

### Messaging & Channels

| Scope | Description |
|-------|-------------|
| `dm_channels.read` | Read user's DMs (requires approval) |
| `messages.read` | Read messages via local RPC |
| `webhook.incoming` | Generate webhook; user picks channel |

### Voice & Other

| Scope | Description |
|-------|-------------|
| `voice` | Connect to voice channels |
| `role_connections.write` | Update user's role connection metadata |

## Gateway Authentication

The bot token is sent in the WebSocket Identify payload (opcode 2), not as an HTTP header:

```json
{
  "op": 2,
  "d": {
    "token": "Bot MTk4NjIy...",
    "intents": 513,
    "properties": {
      "os": "linux",
      "browser": "discord",
      "device": "discord"
    }
  }
}
```

### Gateway Intents

| Intent | Bit | Privileged |
|--------|-----|------------|
| GUILDS | 1 << 0 | No |
| GUILD_MEMBERS | 1 << 1 | Yes |
| GUILD_MODERATION | 1 << 2 | No |
| GUILD_EXPRESSIONS | 1 << 3 | No |
| GUILD_INTEGRATIONS | 1 << 4 | No |
| GUILD_WEBHOOKS | 1 << 5 | No |
| GUILD_INVITES | 1 << 6 | No |
| GUILD_VOICE_STATES | 1 << 7 | No |
| GUILD_PRESENCES | 1 << 8 | Yes |
| GUILD_MESSAGES | 1 << 9 | No |
| GUILD_MESSAGE_REACTIONS | 1 << 10 | No |
| GUILD_MESSAGE_TYPING | 1 << 11 | No |
| DIRECT_MESSAGES | 1 << 12 | No |
| DIRECT_MESSAGE_REACTIONS | 1 << 13 | No |
| DIRECT_MESSAGE_TYPING | 1 << 14 | No |
| MESSAGE_CONTENT | 1 << 15 | Yes |
| GUILD_SCHEDULED_EVENTS | 1 << 16 | No |
| AUTO_MODERATION_CONFIGURATION | 1 << 20 | No |
| AUTO_MODERATION_EXECUTION | 1 << 21 | No |

Privileged intents must be enabled in the Developer Portal. Using an unenabled
privileged intent results in Gateway close code 4014.

## Implementation Notes

1. Token endpoints use **form encoding** — `application/x-www-form-urlencoded`, not JSON
2. The `state` parameter should be cryptographically random, stored server-side
3. Bot tokens are used directly from the portal — no OAuth2 exchange needed
4. Required User-Agent: `DiscordBot (https://url, version)`
5. All HTTP must use TLS 1.2+
6. Gateway rate limit: 120 events per 60 seconds per connection
7. Max payload size: 4096 bytes per Gateway event
