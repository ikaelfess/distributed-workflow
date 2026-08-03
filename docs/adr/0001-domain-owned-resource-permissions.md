# Keep resource permissions in owning contexts

Each domain context owns permission to act on its resources rather than centralizing resource authorization in a shared identity product. Human sign-in is infrastructure (Authelia + Caddy); it only proves a human is logged in. That keeps resource models free of a central permission catalog and avoids coupling every domain to auth internals.
