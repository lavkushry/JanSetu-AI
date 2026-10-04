'use client';
import { createContext, useContext, useEffect, useRef } from 'react';
import { X, LoaderCircle, Inbox, AlertCircle } from 'lucide-react';
import type { Me } from '@/lib/api';
export const SessionContext = createContext<{
  me: Me | null;
  notify: (message: string) => void;
  signIn: () => void;
}>({ me: null, notify: () => {}, signIn: () => {} });
export const useSession = () => useContext(SessionContext);
export function Avatar({
  name,
  size = 'normal',
}: {
  name: string;
  size?: 'normal' | 'small' | 'large';
}) {
  return (
    <span className={`avatar ${size}`} aria-hidden="true">
      {name
        .split(' ')
        .slice(0, 2)
        .map((n) => n[0])
        .join('')}
    </span>
  );
}
export function Modal({
  title,
  children,
  onClose,
  wide = false,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
    const el = ref.current;
    return () => el?.close();
  }, []);
  return (
    <dialog
      ref={ref}
      className={`modal ${wide ? 'wide' : ''}`}
      aria-label={title}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal-heading">
        <h2>{title}</h2>
        <button className="icon-button" aria-label="Close dialog" onClick={onClose}>
          <X size={20} />
        </button>
      </div>
      {children}
    </dialog>
  );
}
export function Loading() {
  return (
    <div className="loading" role="status">
      <LoaderCircle className="spin" size={24} />
      <span>Loading your city…</span>
    </div>
  );
}
export function Empty({ title, children }: { title: string; children?: React.ReactNode }) {
  return (
    <div className="empty">
      <Inbox size={32} />
      <h3>{title}</h3>
      <div>{children}</div>
    </div>
  );
}
export function ErrorState({ error, retry }: { error: Error; retry: () => void }) {
  return (
    <div className="error-box" role="alert">
      <AlertCircle size={20} />
      <div>
        <p>{error.message}</p>
        <button className="text-button" onClick={retry}>
          Try again
        </button>
      </div>
    </div>
  );
}
export function FormError({ error }: { error: unknown }) {
  return error ? (
    <p className="form-error" role="alert">
      {error instanceof Error ? error.message : 'Please try again'}
    </p>
  ) : null;
}
export function Badge({ state }: { state: string }) {
  const good = ['RESOLVED', 'VERIFIED', 'APPROVED'].includes(state);
  return (
    <span className={`badge ${good ? 'good' : state.includes('PENDING') ? 'waiting' : ''}`}>
      {state.toLowerCase().replaceAll('_', ' ')}
    </span>
  );
}
