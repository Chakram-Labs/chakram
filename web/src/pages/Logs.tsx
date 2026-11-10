import { useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import { Pause, Play, Trash2, Radio } from 'lucide-react';
import { Chip } from '../components/ui';

const MAX_LINES = 500;

function lineClass(line: string): string {
  if (/ERROR|error/i.test(line)) return 'text-red-400';
  if (/WARN|warn/i.test(line))   return 'text-amber-400';
  if (/level=debug|DEBUG/i.test(line)) return 'text-gray-600';
  return 'text-gray-400';
}

export default function Logs() {
  const [lines,     setLines]     = useState<string[]>([]);
  const [connected, setConnected] = useState(false);
  const [paused,    setPaused]    = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const esRef     = useRef<EventSource | null>(null);

  function connect() {
    if (esRef.current) esRef.current.close();
    const es = new EventSource(api.logsUrl());
    esRef.current = es;
    setConnected(false);
    es.onopen    = () => setConnected(true);
    es.onmessage = (e) => {
      setLines(prev => {
        const next = [...prev, e.data];
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
      });
    };
    es.onerror = () => {
      setConnected(false);
      es.close();
      setTimeout(connect, 3000);
    };
  }

  useEffect(() => { connect(); return () => esRef.current?.close(); }, []);
  useEffect(() => { if (!paused) bottomRef.current?.scrollIntoView({ behavior: 'smooth' }); }, [lines, paused]);

  return (
    <div className="flex flex-col" style={{ height: 'calc(100vh - 9rem)' }}>

      {/* Toolbar */}
      <div className="rounded-xl overflow-hidden border border-white/8 mb-4 shrink-0">
        <div className="bg-[#161616] px-6 py-3.5 flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <h6 className="text-sm font-semibold text-white">Node Logs</h6>
            <Chip color={connected ? 'lime' : 'red'}>
              <Radio size={9} strokeWidth={2.5} className={connected ? 'animate-pulse' : ''} />
              {connected ? 'Live' : 'Reconnecting…'}
            </Chip>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setPaused(p => !p)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-white/8 px-3 py-1.5 text-xs font-medium text-gray-400 hover:bg-white/5 hover:text-white transition-colors"
            >
              {paused ? <Play size={12} strokeWidth={2} /> : <Pause size={12} strokeWidth={2} />}
              {paused ? 'Resume' : 'Pause'}
            </button>
            <button
              onClick={() => setLines([])}
              className="inline-flex items-center gap-1.5 rounded-lg border border-white/8 px-3 py-1.5 text-xs font-medium text-gray-400 hover:bg-white/5 hover:text-white transition-colors"
            >
              <Trash2 size={12} strokeWidth={2} />
              Clear
            </button>
          </div>
        </div>
      </div>

      {/* Terminal */}
      <div className="flex-1 min-h-0 rounded-xl bg-[#0a0a0a] border border-white/8 overflow-auto p-5">
        {lines.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-gray-600">
            Waiting for log lines…
          </div>
        ) : (
          lines.map((line, i) => (
            <pre key={i} className={`text-xs whitespace-pre-wrap break-all font-mono leading-5 ${lineClass(line)}`}>
              {line}
            </pre>
          ))
        )}
        <div ref={bottomRef} />
      </div>

      {paused && (
        <p className="mt-2 text-center text-xs text-amber-400 shrink-0">
          Paused — new lines buffering
        </p>
      )}
    </div>
  );
}
