import { Suspense } from 'react';
import JanSetu from '@/components/jansetu';
export default function Page() {
  return (
    <Suspense fallback={<div className="loading">Loading JanSetu…</div>}>
      <JanSetu />
    </Suspense>
  );
}
