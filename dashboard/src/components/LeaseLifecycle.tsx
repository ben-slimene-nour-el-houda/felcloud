const STAGES = [
  { state: 'FREE', text: 'Available to allocate' },
  { state: 'ALLOCATED', text: 'Leased to a sandbox' },
  { state: 'QUARANTINED', text: 'Cooling down; cannot be reallocated' },
  { state: 'FREE', text: 'Returned to the pool' },
];

export function LeaseLifecycle() {
  return (
    <ol className="lifecycle" aria-label="Lease lifecycle: free, allocated, quarantined, free">
      {STAGES.map((s, i) => (
        <li key={i} className="lifecycle-step">
          <span className="lifecycle-state">{s.state}</span>
          <span className="muted">{s.text}</span>
          {i < STAGES.length - 1 && (
            <span className="lifecycle-arrow" aria-hidden>
              →
            </span>
          )}
        </li>
      ))}
    </ol>
  );
}
