// Placeholders written by the scaffold; the public-site lane replaces them.
export function HomePage() {
  return <div className='p-6'>Home</div>
}
export function PublicPricingPage() {
  return <div className='p-6'>Pricing</div>
}
export function PublicModelDetailPage({ modelId }: { modelId: string }) {
  return <div className='p-6'>{modelId}</div>
}
export function AboutPage() {
  return <div className='p-6'>About</div>
}
export function LegalPage({ kind }: { kind: 'user-agreement' | 'privacy-policy' }) {
  return <div className='p-6'>{kind}</div>
}
