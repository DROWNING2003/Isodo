export function parseRemoteAPI(value: string): URL;
export function startDesktopServer(options: {staticDir:string;apiURL:string;port?:number}): Promise<{url:string;close():Promise<void>}>;
