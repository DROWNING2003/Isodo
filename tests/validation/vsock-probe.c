/* Disposable transport probe, NOT the Isodo Guest Agent or production protocol. */
#include <sys/socket.h>
#include <linux/vm_sockets.h>
#include <sys/wait.h>
#include <unistd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
int main(void) {
 int s=socket(AF_VSOCK,SOCK_STREAM,0);
 struct sockaddr_vm addr={.svm_family=AF_VSOCK,.svm_cid=VMADDR_CID_ANY,.svm_port=1024};
 if(s<0 || bind(s,(struct sockaddr*)&addr,sizeof(addr)) || listen(s,4)){perror("vsock");return 1;}
 puts("ISODO_PROBE_READY"); fflush(stdout);
 for(;;){int c=accept(s,NULL,NULL);if(c<0)continue;
 char buf[256]={0}; int n=read(c,buf,sizeof(buf)-1);
 if(n>0 && !strncmp(buf,"probe",5)){
 dprintf(c,"READY transport-only\n");
 pid_t p=fork();if(p==0){dup2(c,1);dup2(c,2);close(s);execl("/bin/busybox","busybox","sh","-c","echo ISODO_GUEST_STDOUT; echo ISODO_GUEST_STDERR >&2; uname -r; exit 7",NULL);_exit(127);}
 int status=0;waitpid(p,&status,0);dprintf(c,"EXIT %d\n",WIFEXITED(status)?WEXITSTATUS(status):-1);
 } close(c);}
}
