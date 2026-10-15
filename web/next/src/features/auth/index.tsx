// Placeholders written by the scaffold; the auth lane replaces them.
export function LoginPage({ tenantSlug }: { tenantSlug?: string }) {
  return <div className='p-6'>{tenantSlug ?? 'login'}</div>
}
export function BridgeLoginPage() {
  return <div className='p-6'>bridge</div>
}
export function OidcCallbackPage() {
  return <div className='p-6'>oidc</div>
}
export function RegisterPage() {
  return <div className='p-6'>register</div>
}
export function SetupPage() {
  return <div className='p-6'>setup</div>
}
export function AccountDisabledPage() {
  return <div className='p-6'>disabled</div>
}
export function ForbiddenPage() {
  return <div className='p-6'>forbidden</div>
}
