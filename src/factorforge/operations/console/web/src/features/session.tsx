import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, request, type Session, type Selection } from "../api/client";
type Capabilities = {
  refresh_seconds: number;
  max_retries: number;
  pages: Record<string, string>;
};
type State = {
  session: Session | null;
  loaded: boolean;
  selection: Selection | null;
  selections: Selection[];
  generation: number;
  capabilities: Capabilities | null;
  setSession: (s: Session | null) => void;
  choose: (s: Selection | null) => void;
  invalidate: (selectionOnly?: boolean) => void;
  revalidate: () => Promise<void>;
};
const SessionContext = createContext<State | null>(null);
export function useSession() {
  const c = useContext(SessionContext);
  if (!c) throw new Error("SESSION_CONTEXT");
  return c;
}
export function SessionProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const [session, setSessionValue] = useState<Session | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [selection, setSelection] = useState<Selection | null>(null);
  const [selections, setSelections] = useState<Selection[]>([]);
  const [generation, setGeneration] = useState(0);
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null);
  const chosen = useRef<Selection | null>(null);
  const epoch = useRef(0);
  const clear = () => {
    epoch.current++;
    client.cancelQueries();
    client.clear();
    chosen.current = null;
    setSelection(null);
    setGeneration(epoch.current);
  };
  const choose = (s: Selection | null) => {
    clear();
    chosen.current = s;
    setSelection(s);
  };
  const setSession = (s: Session | null) => {
    clear();
    setSessionValue(s);
    setSelections([]);
    setCapabilities(null);
    setLoaded(true);
  };
  const invalidate = (selectionOnly = false) => {
    if (selectionOnly) {
      choose(null);
      return;
    }
    setSession(null);
  };
  const revalidate = async () => {
    const version = epoch.current;
    try {
      const s = await request<Session>("/session");
      const [allowed, caps] = await Promise.all([
        request<Selection[]>("/selections"),
        request<Capabilities>("/capabilities"),
      ]);
      if (version !== epoch.current) return;
      setSessionValue(s);
      setSelections(allowed);
      setCapabilities(caps);
      const current = chosen.current;
      if (
        current &&
        !allowed.some(
          (x) =>
            x.selection_id === current.selection_id &&
            x.binding_version === current.binding_version &&
            x.environment === current.environment &&
            x.instance_id === current.instance_id,
        )
      )
        choose(null);
      setLoaded(true);
    } catch (e) {
      if (version !== epoch.current) return;
      if (e instanceof ApiError && e.status === 401) setSession(null);
      setLoaded(true);
    }
  };
  useEffect(() => {
    void revalidate();
    const online = () => void revalidate();
    const visible = () => {
      if (document.visibilityState === "visible") void revalidate();
    };
    window.addEventListener("online", online);
    document.addEventListener("visibilitychange", visible);
    return () => {
      epoch.current++;
      client.cancelQueries();
      window.removeEventListener("online", online);
      document.removeEventListener("visibilitychange", visible);
    };
  }, []);
  useEffect(() => {
    if (session) void revalidate();
  }, [session?.user_id]);
  return (
    <SessionContext.Provider
      value={{
        session,
        loaded,
        selection,
        selections,
        generation,
        capabilities,
        setSession,
        choose,
        invalidate,
        revalidate,
      }}
    >
      {children}
    </SessionContext.Provider>
  );
}
export function useVisible() {
  const [visible, setVisible] = useState(
    document.visibilityState === "visible",
  );
  useEffect(() => {
    const update = () => setVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);
  return visible;
}
