import { useEffect, type ReactNode } from "react";
import { Navigate } from "@tanstack/react-router";
import { refreshAuth, useAuth } from "./auth";

export function IsodoAuthGate({ children }: { children: ReactNode }) {
  const auth = useAuth();
  useEffect(() => {
    void refreshAuth();
    const onFocus = () => {
      void refreshAuth();
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, []);
  if (auth.status === "loading")
    return (
      <div role="status" className="p-6 text-sm text-muted-foreground">
        正在确认登录状态…
      </div>
    );
  return auth.status === "authenticated" ? children : <Navigate to="/sandboxes/login" replace />;
}
