import * as RadixToast from '@radix-ui/react-toast';
import { CheckCircle2, Info, X, XCircle } from 'lucide-react';
import { createContext, useCallback, useContext, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';

export type ToastKind = 'info' | 'success' | 'error';

interface ToastItem {
  id: number;
  kind: ToastKind;
  message: string;
}

interface ToastApi {
  toast: (message: string, kind?: ToastKind) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

const icons = { info: Info, success: CheckCircle2, error: XCircle } as const;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const nextId = useRef(0);
  const toast = useCallback((message: string, kind: ToastKind = 'info') => {
    nextId.current += 1;
    const id = nextId.current;
    setItems((cur) => [...cur, { id, kind, message }]);
  }, []);
  const api = useMemo(() => ({ toast }), [toast]);
  return (
    <ToastContext.Provider value={api}>
      <RadixToast.Provider swipeDirection="right" label="Notifications">
        {children}
        {items.map((t) => {
          const Icon = icons[t.kind];
          return (
            <RadixToast.Root
              key={t.id}
              className={`toast toast-${t.kind}`}
              onOpenChange={(open) => {
                if (!open) setItems((cur) => cur.filter((i) => i.id !== t.id));
              }}
            >
              <Icon size={16} className="icon" aria-hidden="true" />
              <RadixToast.Description>{t.message}</RadixToast.Description>
              <RadixToast.Close className="toast-close" aria-label="Dismiss">
                <X size={14} aria-hidden="true" />
              </RadixToast.Close>
            </RadixToast.Root>
          );
        })}
        <RadixToast.Viewport className="toast-viewport" />
      </RadixToast.Provider>
    </ToastContext.Provider>
  );
}

// eslint-disable-next-line react-refresh/only-export-components
export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast fuori da <ToastProvider>');
  return ctx;
}
