import type { ReactNode } from "react";

// ConnectionSection is one group of cards (#search, #downloads, #metadata, #other): its
// title, a line on what it holds, the header's own actions (Add indexer, Sync Prowlarr…),
// and the cards — one column on a phone, two from tablet width.
export function ConnectionSection({ id, title, subtitle, actions, above, children }: {
  id: string;
  title: string;
  subtitle: ReactNode;
  actions?: ReactNode;
  /** Panels between the header and the cards (an Add form, the Prowlarr sync). */
  above?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section id={id} aria-labelledby={`${id}-title`} className="flex scroll-mt-20 flex-col gap-3">
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-2">
        <div className="min-w-0 flex-1 basis-[240px]">
          <h2 id={`${id}-title`} className="m-0 text-[14px] font-bold">{title}</h2>
          <p className="m-0 mt-0.5 text-[11.5px] text-ink-faint">{subtitle}</p>
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {above}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">{children}</div>
    </section>
  );
}
