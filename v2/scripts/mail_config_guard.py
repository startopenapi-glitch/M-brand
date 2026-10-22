#!/usr/bin/env python3
"""Check only SMTP sender configuration; never send mail or print addresses/keys."""
import argparse
from email.utils import parseaddr
from pathlib import Path
import re
import shlex

SENDER_KEYS=('ZEROBRAND_SMTP_FROM','RESEND_FROM','SMTP_FROM')
ADDRESS=re.compile(r'[^@\s<>,;]+@[^@\s<>,;]+\.[^@\s<>,;]+')

def validate_senders(config):
 configured=[(key,config.get(key,'')) for key in SENDER_KEYS if config.get(key,'').strip()]
 if not configured:raise RuntimeError('SMTP sender is missing; restore the verified mail configuration')
 for key,value in configured:
  display,address=parseaddr(value)
  if display or value!=address or not ADDRESS.fullmatch(value):
   raise RuntimeError(key+' must contain one plain email address without a display name; SMTP would reject the envelope sender')
 return True

def validate_file(path):
 try:
  config={}
  for line in Path(path).read_text().splitlines():
   match=re.match(r'^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$',line)
   if not match or match.group(1) not in SENDER_KEYS:continue
   values=shlex.split(match.group(2),comments=True)
   if len(values)>1:raise ValueError('ambiguous sender')
   config[match.group(1)]=values[0] if values else ''
 except (OSError,UnicodeError,ValueError) as error:
  raise RuntimeError('Cannot safely read sender configuration; no config values are exposed') from None
 return validate_senders(config)

def main():
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('env_file');args=parser.parse_args()
 try:validate_file(args.env_file)
 except RuntimeError as error:parser.exit(1,str(error)+'\n')
 print('SMTP sender configuration verified; no email sent')

if __name__=='__main__':main()
