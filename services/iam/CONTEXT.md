# Identity and Access

The Identity and Access context establishes who a human user or internal service is and governs platform-wide access. Permission to act on a domain resource belongs to the context that owns that resource.

## Language

**User**:
A human individual who accesses the platform. Non-human service identities are a separate concept.
_Avoid_: Account, principal

**Service Identity**:
A non-human identity assigned to an internal service for authentication. It is never modeled as a User.
_Avoid_: Service user, machine user

**Administrator**:
A trusted User with the Administrator Access Level who may grant administrator access. Administrator access is assigned only by an existing Administrator or a bootstrap process, never through public registration.
_Avoid_: Admin user

**Suspended User**:
A User who cannot authenticate or access the platform but whose stable identity is retained for historical attribution. Suspension invalidates all existing Access Tokens immediately.
_Avoid_: Deleted user, deactivated account

**Access Level**:
A platform-wide classification of a User's authority: Standard or Administrator. It does not apply to Service Identities or describe permission to act on resources owned by another context.
_Avoid_: Role, user role

**Email Address**:
A unique, changeable sign-in identifier for a User. Changing it does not create a new User.
_Avoid_: Username, user identity

**Access Token**:
Short-lived proof that a User or Service Identity authenticated. For a User, it represents the current Access Level; changing that level invalidates previously issued tokens immediately.
_Avoid_: Session

**Resource Permission**:
Authority to act on a resource such as a workflow. It belongs to the context that owns the resource, not to Identity and Access.
_Avoid_: IAM permission
