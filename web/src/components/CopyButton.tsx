import { Check, Copy } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { Button } from './Button';

export interface CopyButtonProps {
  value: string;
  label?: string;
}

export function CopyButton({ value, label = 'Copy' }: CopyButtonProps) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setFailed(false);
    } catch {
      setFailed(true);
      setCopied(false);
    }
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      setCopied(false);
      setFailed(false);
    }, 2000);
  }

  return (
    <>
      <Button size="sm" onClick={() => void copy()}>
        {copied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
        {copied ? 'Copied' : failed ? 'Copy failed' : label}
      </Button>
      <span className="sr-only" role="status">
        {copied ? 'Copied to clipboard' : failed ? 'Could not copy' : ''}
      </span>
    </>
  );
}
