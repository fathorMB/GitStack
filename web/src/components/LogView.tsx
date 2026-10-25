export interface LogViewLine {
  key: string | number;
  prefix: string;
  text: string;
  cls?: string;
}

export function LogView({ lines, label, live, loadingText, emptyText }: {
  lines: LogViewLine[];
  label: string;
  live: boolean;
  loadingText?: string;
  emptyText?: string;
}) {
  return (
    <div className="log" role="log" aria-label={label} aria-live={live ? 'polite' : 'off'}>
      {loadingText && lines.length === 0 ? <div>{loadingText}</div> : null}
      {!loadingText && emptyText && lines.length === 0 ? <div>{emptyText}</div> : null}
      {lines.map((line) => (
        <div key={line.key} className={line.cls || undefined}>
          <span className="n">{line.prefix}</span>{line.text}
        </div>
      ))}
      {live ? <div aria-hidden="true">▍</div> : null}
    </div>
  );
}
