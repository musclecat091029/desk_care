"""Attach the DeskCare Windows icon resource to a Go-built unsigned PE exe.

Usage: python scripts/embed_windows_icon.py deskcare_window_1.9.exe
This script is used only to build releases; end users need no Python.
Resource section is derived from the bundled DeskCare cat icon.
"""
from pathlib import Path
import struct
import sys

DIR_RESOURCE=2
SECTION_ALIGN=0x1000
FILE_ALIGN=0x200

def u16(b,o):return struct.unpack_from('<H',b,o)[0]
def u32(b,o):return struct.unpack_from('<I',b,o)[0]
def setu16(b,o,v):struct.pack_into('<H',b,o,v)
def setu32(b,o,v):struct.pack_into('<I',b,o,v)
def align(n,a):return (n+a-1)//a*a

def peinfo(data):
    if data[:2]!=b'MZ':raise ValueError('not a Windows PE executable')
    pe=u32(data,0x3c)
    if data[pe:pe+4]!=b'PE\x00\x00':raise ValueError('invalid PE signature')
    n=u16(data,pe+6)
    op=pe+24
    if u16(data,op)!=0x20b:raise ValueError('expected 64-bit Windows PE')
    sect=op+u16(data,pe+20)
    dirs=op+112
    results=[]
    for i in range(n):
        o=sect+i*40
        results.append({'name':bytes(data[o:o+8]).rstrip(b'\0'), 'vs':u32(data,o+8),'va':u32(data,o+12), 'rs':u32(data,o+16),'rp':u32(data,o+20),'ch':u32(data,o+36)})
    return pe,op,sect,dirs,results

def relocate_resource(data, diff):
    """Resource tree stores relative directory offsets and absolute RVAs in leaf data entries."""
    found=[]
    def walk(offset, stack=()):
        if offset in stack: raise ValueError('resource directory cycle')
        if offset+16>len(data):raise ValueError('invalid resource tree')
        count=u16(data,offset+12)+u16(data,offset+14)
        if count>1000:raise ValueError('invalid resource entry count')
        for i in range(count):
            eo=offset+16+i*8
            if eo+8>len(data): raise ValueError('out-of-bounds resource table')
            nextoff=u32(data,eo+4)
            if nextoff & 0x80000000:
                walk(nextoff&0x7fffffff,stack+(offset,))
            else:
                if nextoff+16>len(data):raise ValueError('invalid data entry')
                old=u32(data,nextoff)
                setu32(data,nextoff,old+diff)
                found.append((old,old+diff))
    walk(0)
    return found

def embed_icon(exe_path,payload_path):
    exe=bytearray(Path(exe_path).read_bytes())
    pe,op,sect,dirs,ss=peinfo(exe)
    if any(s['name']==b'.rsrc' for s in ss):raise ValueError('already has a resource section')
    count=len(ss)
    new_hdr=sect+count*40
    header_size=u32(exe,op+60)
    if new_hdr+40>header_size: raise ValueError('no space to add section header')
    f_align=u32(exe,op+36); s_align=u32(exe,op+32)
    assert f_align==FILE_ALIGN and s_align==SECTION_ALIGN, (f_align,s_align)
    raw=Path(payload_path).read_bytes()
    if raw[:4]!=b'\x00'*4: raise ValueError('incorrect resource payload')
    # The original icon resource was from the signed-off 1.6.1 build.
    old_va=0x64a000
    new_va=align(max(s['va']+max(s['vs'],s['rs']) for s in ss),s_align)
    new_rp=align(len(exe),f_align)
    res_data=bytearray(raw)
    leaves=relocate_resource(res_data,new_va-old_va)
    if len(leaves)!=8: raise ValueError('expected 7 icon sizes and group icon; found %d'%len(leaves))
    orig_virtual_size=0x27c6c
    raw_sz=align(len(res_data),f_align)
    h=bytearray(40)
    h[:8]=b'.rsrc\0\0\0'
    setu32(h,8,orig_virtual_size)
    setu32(h,12,new_va)
    setu32(h,16,raw_sz)
    setu32(h,20,new_rp)
    setu32(h,36,0x40000040) # IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ
    exe[new_hdr:new_hdr+40]=h
    setu16(exe,pe+6,count+1)
    setu32(exe,op+56,align(new_va+orig_virtual_size,s_align))
    setu32(exe,dirs+DIR_RESOURCE*8,new_va)
    setu32(exe,dirs+DIR_RESOURCE*8+4,orig_virtual_size)
    setu32(exe,op+64,0) # Windows loader can calculate optional checksum
    exe+=b'\0'*(new_rp-len(exe))
    exe+=res_data
    exe+=b'\0'*(raw_sz-len(res_data))
    Path(exe_path).write_bytes(exe)
    pe,op,sect,dirs,ss=peinfo(exe)
    assert ss[-1]['name']==b'.rsrc' and u32(exe,dirs+16)==new_va
    print('Updated PE icon:',exe_path, 'resource entries',len(leaves),'size',len(exe))

if __name__=='__main__':
    embed_icon(sys.argv[1],Path(__file__).resolve().parent.parent / 'assets' / 'windows' / 'deskcare_icon_resource.bin')
