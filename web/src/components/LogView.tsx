import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button } from './Button';

export interface LogViewLine {
  key: string | number;
  prefix: string;
  text: string;
  cls?: string;
}

const BOTTOM_SLACK = 24;

export function LogView({ lines, label, live, loadingText, emptyText }: {
  lines: LogViewLine[];
  label: string;
  live: boolean;
  loadingText?: string;
  emptyText?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [following, setFollowing] = useState(true);
  const followingRef = useRef(true);

  const setFollow = useCallback((v: boolean) => {
    followingRef.current = v;
    setFollowing(v);
  }, []);

  const scrollToEnd = useCallback(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, []);

  // Segue l'ultima riga solo a job in corso e finché l'utente non scorre in su.
  useLayoutEffect(() => {
    if (live && followingRef.current) scrollToEnd();
  }, [lines.length, live, scrollToEnd]);

  // Un nuovo job (o un job che riparte) riparte dal segui.
  useEffect(() => {
    if (live) setFollow(true);
  }, [live, setFollow]);

  const onScroll = () => {
    const el = ref.current;
    if (!el || !live) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight <= BOTTOM_SLACK;
    if (atBottom !== followingRef.current) setFollow(atBottom);
  };

  return (
    <>
      <div ref={ref} onScroll={onScroll} className="log" role="log" aria-label={label} aria-live={live ? 'polite' : 'off'}>
        {loadingText && lines.length === 0 ? <div>{loadingText}</div> : null}
        {!loadingText && emptyText && lines.length === 0 ? <div>{emptyText}</div> : null}
        {lines.map((line) => (
          <div key={line.key} className={['l', line.cls].filter(Boolean).join(' ')}>
            <span className="n">{line.prefix}</span>{line.text}
          </div>
        ))}
        {live ? <div aria-hidden="true">▍</div> : null}
      </div>
      {live && !following ? (
        <div className="log-follow">
          <Button size="sm" onClick={() => { setFollow(true); scrollToEnd(); }}>Follow / jump to bottom</Button>
        </div>
      ) : null}
    </>
  );
}
