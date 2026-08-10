/** One entry from GET /api/me/links. */
export interface ExternalIdentityLink {
  provider: string;
  external_id: string;
}

export interface ListExternalIdentityLinksResponse {
  links: ExternalIdentityLink[];
}

/** Response from POST /api/me/links/telegram/start (and /auth/telegram/start). */
export interface TelegramLinkStartResponse {
  nonce: string;
  deep_link: string;
}
