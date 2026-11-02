package orchestrator

import (
	"context"
	"strconv"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/skillartifact"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *CHVServer) SyncSkills(ctx context.Context, request *nodev1.SyncSkillsRequest) (*nodev1.SyncSkillsResponse, error) {
	if request == nil || request.Version == 0 || len(request.Skills) > skillartifact.MaxSkills {
		return nil, status.Error(codes.InvalidArgument, "invalid skill set")
	}
	documents := []skillartifact.Skill{}
	names := map[string]bool{}
	ids := map[string]bool{}
	for _, d := range request.Skills {
		if d == nil {
			return nil, status.Error(codes.InvalidArgument, "invalid skill")
		}
		v := skillartifact.Skill{ID: d.Id, Name: d.Name, Description: d.Description, Body: d.Body}
		if !canonicalUUID(d.Id) || v.Validate() != nil || names[d.Name] || ids[d.Id] {
			return nil, status.Error(codes.InvalidArgument, "invalid or duplicate skill")
		}
		names[d.Name] = true
		ids[d.Id] = true
		documents = append(documents, v)
	}
	guest, err := s.fileGuest(ctx, request.Target)
	if err != nil {
		return nil, err
	}
	defer guest.close()
	data, encodeErr := skillartifact.Encode(documents)
	if encodeErr != nil {
		return nil, status.Error(codes.InvalidArgument, "combined Skill content exceeds 96 KiB")
	}
	// The command and path are Node-owned. Content is JSON data, never shell code.
	_, err = runT3GuestCommand(ctx, guest.client, guest.identity, "skills-"+newT3RequestSuffix(), []string{"python3", "-c", skillInstall, string(data), strconv.FormatUint(request.Version, 10), strconv.FormatUint(request.Target.OwnershipGeneration, 10), strconv.FormatUint(request.Target.Revision, 10)})
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "skill sync failed; check managed path conflicts and retry")
	}
	if err = guest.check(); err != nil {
		return nil, err
	}
	return &nodev1.SyncSkillsResponse{RestartRequired: true}, nil
}

// Use directory descriptors and O_NOFOLLOW throughout. Only owned instruction
// files may be replaced/removed; user files and other skills are never traversed.
const skillInstall = `import os,sys,json,stat,fcntl,secrets,re,hashlib
root=os.open('/workspace',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
def directory(parent,name):
 try:
  os.mkdir(name,0o755,dir_fd=parent)
  os.chown(name,1000,1000,dir_fd=parent,follow_symlinks=False)
 except FileExistsError: pass
 return os.open(name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=parent)
a=directory(root,'.agents');d=directory(a,'skills')
l=os.open('.isodo-skills.lock',os.O_CREAT|os.O_RDWR|os.O_NOFOLLOW|os.O_NONBLOCK,0o600,dir_fd=d)
assert stat.S_ISREG(os.fstat(l).st_mode)
fcntl.flock(l,fcntl.LOCK_EX|fcntl.LOCK_NB)
def read(parent,name):
 f=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=parent)
 assert stat.S_ISREG(os.fstat(f).st_mode)
 with os.fdopen(f) as stream:
  text=stream.read(2097153)
 assert len(text)<=2097152
 return text
def write(parent,name,text):
 temp='.isodo-write-'+secrets.token_hex(12)
 f=os.open(temp,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o644,dir_fd=parent)
 try:
  with os.fdopen(f,'w') as stream: stream.write(text);stream.flush();os.fsync(stream.fileno())
  os.chown(temp,1000,1000,dir_fd=parent,follow_symlinks=False)
  os.replace(temp,name,src_dir_fd=parent,dst_dir_fd=parent);os.fsync(parent)
 finally:
  try: os.unlink(temp,dir_fd=parent)
  except FileNotFoundError: pass
skills=json.loads(sys.argv[1]);version=int(sys.argv[2]);generation=int(sys.argv[3]);revision=int(sys.argv[4])
assert isinstance(skills,list) and len(skills)<=32
ids=set();names=set()
for s in skills:
 assert re.fullmatch(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}',s['id'])
 assert re.fullmatch(r'[a-z][a-z0-9]*(?:-[a-z0-9]+)*',s['name']) and len(s['name'])<=64
 assert s['id'] not in ids and s['name'] not in names
 ids.add(s['id']);names.add(s['name'])
try: old=json.loads(read(d,'.isodo-skills.json'))
except FileNotFoundError: old={'generation':0,'revision':0,'version':0,'ids':[]}
assert (generation,revision,version)>=(old['generation'],old['revision'],old['version']),'stale skill revision'
# Preflight every existing managed directory before any mutation.
previous=set(old['ids'])
for identifier in previous:
 assert re.fullmatch(r'[0-9a-f-]{36}',identifier)
for identifier in previous|ids:
 name='isodo-'+identifier
 try: sd=os.open(name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=d)
 except FileNotFoundError: continue
 try:
  assert set(os.listdir(sd))<= {'SKILL.md','.isodo-owned.json'},'unexpected files in managed Skill'
  marker=json.loads(read(sd,'.isodo-owned.json'))
  assert marker['id']==identifier
  content=read(sd,'SKILL.md')
  assert hashlib.sha256(content.encode()).hexdigest()==marker['sha256'],'managed Skill modified'
 finally: os.close(sd)
for s in skills:
 sd=directory(d,'isodo-'+s['id'])
 try:
  content=s['content']
  write(sd,'SKILL.md',content)
  write(sd,'.isodo-owned.json',json.dumps({'id':s['id'],'sha256':hashlib.sha256(content.encode()).hexdigest()}))
 finally: os.close(sd)
for identifier in previous-ids:
 name='isodo-'+identifier
 try: sd=os.open(name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=d)
 except FileNotFoundError: continue
 try:
  os.unlink('SKILL.md',dir_fd=sd);os.unlink('.isodo-owned.json',dir_fd=sd)
 finally: os.close(sd)
 os.rmdir(name,dir_fd=d)
write(d,'.isodo-skills.json',json.dumps({'generation':generation,'revision':revision,'version':version,'ids':sorted(ids)}))
print('Skill files synchronized; start a new Agent task to load changes.')
`
