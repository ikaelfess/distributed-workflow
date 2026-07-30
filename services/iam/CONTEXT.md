# Identity and Access

The Identity and Access context establishes who a human user or internal service is and governs platform-wide access. Permission to act on a domain resource belongs to the context that owns that resource.

## Language

**User**:
A human individual who accesses the platform. Non-human service identities are a separate concept.
_Avoid_: Account, principal

**Unverified User**:
A registered User who has not yet proved control of their Email Address and therefore cannot authenticate.
_Avoid_: Pending user, inactive account

**Service Identity**:
A non-human identity assigned to an internal service for authentication. It is never modeled as a User.
_Avoid_: Service user, machine user

**Administrator**:
A verified, active User with the Administrator Access Level who may administer identities and Access Levels. Administrator access is assigned only by an existing Administrator or a trusted bootstrap process, never through public registration, and does not automatically grant access to resources owned by another context.
_Avoid_: Admin user

**Suspended User**:
A User who cannot authenticate or access the platform but whose stable identity is retained for historical attribution. Suspension immediately revokes all of the User's Authentication Sessions and Access Tokens without undoing email verification.
_Avoid_: Deleted user, deactivated account

**Access Level**:
A platform-wide classification of a User's authority: Standard or Administrator. It does not apply to Service Identities or describe permission to act on resources owned by another context.
_Avoid_: Role, user role

**Email Address**:
A unique, case-insensitive, changeable sign-in identifier for a User. Changing it does not create a new User and requires proof of control before the new address becomes active.
_Avoid_: Username, user identity

**Authentication Session**:
A revocable continuity of authenticated access established by one successful User login. A User may have multiple concurrent Authentication Sessions.
_Avoid_: Login session, Access Token

**Access Token**:
Short-lived proof that a User or Service Identity authenticated. For a User, it represents the current Access Level; changing that level immediately revokes all existing Authentication Sessions and Access Tokens.
_Avoid_: Session token, Authentication Session

**Resource Permission**:
Authority to act on a resource such as a workflow. It belongs to the context that owns the resource, not to Identity and Access.
_Avoid_: IAM permission
