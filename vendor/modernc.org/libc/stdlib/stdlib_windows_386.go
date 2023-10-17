// Code generated by 'ccgo stdlib/gen.c -crt-import-path "" -export-defines "" -export-enums "" -export-externs X -export-fields F -export-structs "" -export-typedefs "" -header -hide _OSSwapInt16,_OSSwapInt32,_OSSwapInt64 -ignore-unsupported-alignment -o stdlib/stdlib_windows_386.go -pkgname stdlib', DO NOT EDIT.

package stdlib

import (
	"math"
	"reflect"
	"sync/atomic"
	"unsafe"
)

var _ = math.Pi
var _ reflect.Kind
var _ atomic.Value
var _ unsafe.Pointer

const (
	CHAR_BIT                                        = 8                    // limits.h:64:1:
	CHAR_MAX                                        = 127                  // limits.h:99:1:
	CHAR_MIN                                        = -128                 // limits.h:97:1:
	DUMMYSTRUCTNAME                                 = 0                    // _mingw.h:519:1:
	DUMMYSTRUCTNAME1                                = 0                    // _mingw.h:520:1:
	DUMMYSTRUCTNAME2                                = 0                    // _mingw.h:521:1:
	DUMMYSTRUCTNAME3                                = 0                    // _mingw.h:522:1:
	DUMMYSTRUCTNAME4                                = 0                    // _mingw.h:523:1:
	DUMMYSTRUCTNAME5                                = 0                    // _mingw.h:524:1:
	DUMMYUNIONNAME                                  = 0                    // _mingw.h:497:1:
	DUMMYUNIONNAME1                                 = 0                    // _mingw.h:498:1:
	DUMMYUNIONNAME2                                 = 0                    // _mingw.h:499:1:
	DUMMYUNIONNAME3                                 = 0                    // _mingw.h:500:1:
	DUMMYUNIONNAME4                                 = 0                    // _mingw.h:501:1:
	DUMMYUNIONNAME5                                 = 0                    // _mingw.h:502:1:
	DUMMYUNIONNAME6                                 = 0                    // _mingw.h:503:1:
	DUMMYUNIONNAME7                                 = 0                    // _mingw.h:504:1:
	DUMMYUNIONNAME8                                 = 0                    // _mingw.h:505:1:
	DUMMYUNIONNAME9                                 = 0                    // _mingw.h:506:1:
	EXIT_FAILURE                                    = 1                    // stdlib.h:45:1:
	EXIT_SUCCESS                                    = 0                    // stdlib.h:44:1:
	INT_MAX                                         = 2147483647           // limits.h:120:1:
	INT_MIN                                         = -2147483648          // limits.h:118:1:
	LLONG_MAX                                       = 9223372036854775807  // limits.h:142:1:
	LLONG_MIN                                       = -9223372036854775808 // limits.h:140:1:
	LONG_LONG_MAX                                   = 9223372036854775807  // limits.h:154:1:
	LONG_LONG_MIN                                   = -9223372036854775808 // limits.h:152:1:
	LONG_MAX                                        = 2147483647           // limits.h:131:1:
	LONG_MIN                                        = -2147483648          // limits.h:129:1:
	MB_LEN_MAX                                      = 5                    // limits.h:35:1:
	MINGW_DDK_H                                     = 0                    // _mingw_ddk.h:2:1:
	MINGW_HAS_DDK_H                                 = 1                    // _mingw_ddk.h:4:1:
	MINGW_HAS_SECURE_API                            = 1                    // _mingw.h:602:1:
	MINGW_SDK_INIT                                  = 0                    // _mingw.h:598:1:
	PATH_MAX                                        = 260                  // limits.h:20:1:
	RAND_MAX                                        = 0x7fff               // stdlib.h:106:1:
	SCHAR_MAX                                       = 127                  // limits.h:75:1:
	SCHAR_MIN                                       = -128                 // limits.h:73:1:
	SHRT_MAX                                        = 32767                // limits.h:106:1:
	SHRT_MIN                                        = -32768               // limits.h:104:1:
	SIZE_MAX                                        = 4294967295           // limits.h:78:1:
	SSIZE_MAX                                       = 2147483647           // limits.h:86:1:
	UCHAR_MAX                                       = 255                  // limits.h:82:1:
	UINT_MAX                                        = 4294967295           // limits.h:124:1:
	ULLONG_MAX                                      = 18446744073709551615 // limits.h:146:1:
	ULONG_LONG_MAX                                  = 18446744073709551615 // limits.h:158:1:
	ULONG_MAX                                       = 4294967295           // limits.h:135:1:
	UNALIGNED                                       = 0                    // _mingw.h:384:1:
	USE___UUIDOF                                    = 0                    // _mingw.h:77:1:
	USHRT_MAX                                       = 65535                // limits.h:113:1:
	WIN32                                           = 1                    // <predefined>:258:1:
	WINNT                                           = 1                    // <predefined>:306:1:
	X_AGLOBAL                                       = 0                    // _mingw.h:346:1:
	X_ALLOCA_S_HEAP_MARKER                          = 0xDDDD               // malloc.h:137:1:
	X_ALLOCA_S_MARKER_SIZE                          = 8                    // malloc.h:140:1:
	X_ALLOCA_S_STACK_MARKER                         = 0xCCCC               // malloc.h:136:1:
	X_ALLOCA_S_THRESHOLD                            = 1024                 // malloc.h:135:1:
	X_ANONYMOUS_STRUCT                              = 0                    // _mingw.h:474:1:
	X_ANONYMOUS_UNION                               = 0                    // _mingw.h:473:1:
	X_ARGMAX                                        = 100                  // _mingw.h:402:1:
	X_CALL_REPORTFAULT                              = 0x2                  // stdlib.h:139:1:
	X_CONST_RETURN                                  = 0                    // _mingw.h:377:1:
	X_CRTNOALIAS                                    = 0                    // corecrt.h:29:1:
	X_CRTRESTRICT                                   = 0                    // corecrt.h:33:1:
	X_CRT_ABS_DEFINED                               = 0                    // stdlib.h:410:1:
	X_CRT_ALGO_DEFINED                              = 0                    // stdlib.h:433:1:
	X_CRT_ALLOCATION_DEFINED                        = 0                    // stdlib.h:528:1:
	X_CRT_ALTERNATIVE_IMPORTED                      = 0                    // _mingw.h:313:1:
	X_CRT_ATOF_DEFINED                              = 0                    // stdlib.h:424:1:
	X_CRT_DOUBLE_DEC                                = 0                    // stdlib.h:72:1:
	X_CRT_ERRNO_DEFINED                             = 0                    // stdlib.h:153:1:
	X_CRT_MANAGED_HEAP_DEPRECATE                    = 0                    // _mingw.h:361:1:
	X_CRT_PACKING                                   = 8                    // corecrt.h:14:1:
	X_CRT_PERROR_DEFINED                            = 0                    // stdlib.h:648:1:
	X_CRT_SECURE_CPP_OVERLOAD_SECURE_NAMES          = 0                    // _mingw_secapi.h:34:1:
	X_CRT_SECURE_CPP_OVERLOAD_SECURE_NAMES_MEMORY   = 0                    // _mingw_secapi.h:35:1:
	X_CRT_SECURE_CPP_OVERLOAD_STANDARD_NAMES        = 0                    // _mingw_secapi.h:36:1:
	X_CRT_SECURE_CPP_OVERLOAD_STANDARD_NAMES_COUNT  = 0                    // _mingw_secapi.h:37:1:
	X_CRT_SECURE_CPP_OVERLOAD_STANDARD_NAMES_MEMORY = 0                    // _mingw_secapi.h:38:1:
	X_CRT_SWAB_DEFINED                              = 0                    // stdlib.h:716:1:
	X_CRT_SYSTEM_DEFINED                            = 0                    // stdlib.h:518:1:
	X_CRT_TERMINATE_DEFINED                         = 0                    // stdlib.h:387:1:
	X_CRT_USE_WINAPI_FAMILY_DESKTOP_APP             = 0                    // corecrt.h:501:1:
	X_CRT_WPERROR_DEFINED                           = 0                    // stdlib.h:677:1:
	X_CRT_WSYSTEM_DEFINED                           = 0                    // stdlib.h:587:1:
	X_CVTBUFSIZE                                    = 349                  // stdlib.h:611:1:
	X_DIV_T_DEFINED                                 = 0                    // stdlib.h:58:1:
	X_DLL                                           = 0                    // _mingw.h:326:1:
	X_ERRCODE_DEFINED                               = 0                    // corecrt.h:117:1:
	X_FILE_OFFSET_BITS                              = 64                   // <builtin>:25:1:
	X_FREEA_INLINE                                  = 0                    // malloc.h:161:1:
	X_FREEENTRY                                     = 0                    // malloc.h:40:1:
	X_GCC_LIMITS_H_                                 = 0                    // limits.h:30:1:
	X_HEAPBADBEGIN                                  = -3                   // malloc.h:34:1:
	X_HEAPBADNODE                                   = -4                   // malloc.h:35:1:
	X_HEAPBADPTR                                    = -6                   // malloc.h:37:1:
	X_HEAPEMPTY                                     = -1                   // malloc.h:32:1:
	X_HEAPEND                                       = -5                   // malloc.h:36:1:
	X_HEAPINFO_DEFINED                              = 0                    // malloc.h:44:1:
	X_HEAPOK                                        = -2                   // malloc.h:33:1:
	X_HEAP_MAXREQ                                   = 0xFFFFFFE0           // malloc.h:20:1:
	X_I16_MAX                                       = 32767                // limits.h:54:1:
	X_I16_MIN                                       = -32768               // limits.h:53:1:
	X_I32_MAX                                       = 2147483647           // limits.h:58:1:
	X_I32_MIN                                       = -2147483648          // limits.h:57:1:
	X_I64_MAX                                       = 9223372036854775807  // limits.h:71:1:
	X_I64_MIN                                       = -9223372036854775808 // limits.h:70:1:
	X_I8_MAX                                        = 127                  // limits.h:50:1:
	X_I8_MIN                                        = -128                 // limits.h:49:1:
	X_ILP32                                         = 1                    // <predefined>:211:1:
	X_INC_CORECRT                                   = 0                    // corecrt.h:8:1:
	X_INC_CORECRT_WSTDLIB                           = 0                    // corecrt_wstdlib.h:7:1:
	X_INC_CRTDEFS                                   = 0                    // crtdefs.h:8:1:
	X_INC_CRTDEFS_MACRO                             = 0                    // _mingw_mac.h:8:1:
	X_INC_LIMITS                                    = 0                    // limits.h:9:1:
	X_INC_MINGW_SECAPI                              = 0                    // _mingw_secapi.h:8:1:
	X_INC_STDLIB                                    = 0                    // stdlib.h:7:1:
	X_INC_STDLIB_S                                  = 0                    // stdlib_s.h:7:1:
	X_INC_VADEFS                                    = 0                    // vadefs.h:7:1:
	X_INC__MINGW_H                                  = 0                    // _mingw.h:8:1:
	X_INT128_DEFINED                                = 0                    // _mingw.h:237:1:
	X_INTEGRAL_MAX_BITS                             = 64                   // <predefined>:320:1:
	X_INTPTR_T_DEFINED                              = 0                    // corecrt.h:62:1:
	X_LIMITS_H___                                   = 0                    // limits.h:60:1:
	X_MALLOC_H_                                     = 0                    // malloc.h:7:1:
	X_MAX_DIR                                       = 256                  // stdlib.h:129:1:
	X_MAX_DRIVE                                     = 3                    // stdlib.h:128:1:
	X_MAX_ENV                                       = 32767                // stdlib.h:141:1:
	X_MAX_EXT                                       = 256                  // stdlib.h:131:1:
	X_MAX_FNAME                                     = 256                  // stdlib.h:130:1:
	X_MAX_PATH                                      = 260                  // stdlib.h:127:1:
	X_MAX_WAIT_MALLOC_CRT                           = 60000                // malloc.h:108:1:
	X_MM_MALLOC_H_INCLUDED                          = 0                    // malloc.h:61:1:
	X_MT                                            = 0                    // _mingw.h:330:1:
	X_M_IX86                                        = 600                  // _mingw_mac.h:54:1:
	X_ONEXIT_T_DEFINED                              = 0                    // stdlib.h:48:1:
	X_OUT_TO_DEFAULT                                = 0                    // stdlib.h:133:1:
	X_OUT_TO_MSGBOX                                 = 2                    // stdlib.h:135:1:
	X_OUT_TO_STDERR                                 = 1                    // stdlib.h:134:1:
	X_PGLOBAL                                       = 0                    // _mingw.h:342:1:
	X_PTRDIFF_T_                                    = 0                    // corecrt.h:90:1:
	X_PTRDIFF_T_DEFINED                             = 0                    // corecrt.h:88:1:
	X_QSORT_S_DEFINED                               = 0                    // stdlib_s.h:40:1:
	X_REPORT_ERRMODE                                = 3                    // stdlib.h:136:1:
	X_RSIZE_T_DEFINED                               = 0                    // corecrt.h:58:1:
	X_SECURECRT_FILL_BUFFER_PATTERN                 = 0xFD                 // _mingw.h:349:1:
	X_SIZE_T_DEFINED                                = 0                    // corecrt.h:37:1:
	X_SSIZE_T_DEFINED                               = 0                    // corecrt.h:47:1:
	X_TAGLC_ID_DEFINED                              = 0                    // corecrt.h:447:1:
	X_THREADLOCALEINFO                              = 0                    // corecrt.h:456:1:
	X_TIME32_T_DEFINED                              = 0                    // corecrt.h:122:1:
	X_TIME64_T_DEFINED                              = 0                    // corecrt.h:127:1:
	X_TIME_T_DEFINED                                = 0                    // corecrt.h:139:1:
	X_UI16_MAX                                      = 0xffff               // limits.h:55:1:
	X_UI32_MAX                                      = 0xffffffff           // limits.h:59:1:
	X_UI64_MAX                                      = 0xffffffffffffffff   // limits.h:72:1:
	X_UI8_MAX                                       = 0xff                 // limits.h:51:1:
	X_UINTPTR_T_DEFINED                             = 0                    // corecrt.h:75:1:
	X_USEDENTRY                                     = 1                    // malloc.h:41:1:
	X_USE_32BIT_TIME_T                              = 0                    // _mingw.h:372:1:
	X_VA_LIST_DEFINED                               = 0                    // <builtin>:55:1:
	X_W64                                           = 0                    // _mingw.h:296:1:
	X_WCHAR_T_DEFINED                               = 0                    // corecrt.h:101:1:
	X_WCTYPE_T_DEFINED                              = 0                    // corecrt.h:108:1:
	X_WIN32                                         = 1                    // <predefined>:164:1:
	X_WIN32_WINNT                                   = 0x502                // _mingw.h:233:1:
	X_WINT_T                                        = 0                    // corecrt.h:110:1:
	X_WRITE_ABORT_MSG                               = 0x1                  // stdlib.h:138:1:
	X_WSTDLIBP_DEFINED                              = 0                    // stdlib.h:673:1:
	X_WSTDLIB_DEFINED                               = 0                    // stdlib.h:553:1:
	X_X86_                                          = 1                    // <predefined>:169:1:
	I386                                            = 1                    // <predefined>:171:1:
)

type Ptrdiff_t = int32 /* <builtin>:3:26 */

type Size_t = uint32 /* <builtin>:9:23 */

type Wchar_t = uint16 /* <builtin>:15:24 */

type X__builtin_va_list = uintptr /* <builtin>:46:14 */
type X__float128 = float64        /* <builtin>:47:21 */

type Va_list = X__builtin_va_list /* <builtin>:50:27 */

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// This macro holds an monotonic increasing value, which indicates
//    a specific fix/patch is present on trunk.  This value isn't related to
//    minor/major version-macros.  It is increased on demand, if a big
//    fix was applied to trunk.  This macro gets just increased on trunk.  For
//    other branches its value won't be modified.

// mingw.org's version macros: these make gcc to define
//    MINGW32_SUPPORTS_MT_EH and to use the _CRT_MT global
//    and the __mingwthr_key_dtor() function from the MinGW
//    CRT in its private gthr-win32.h header.

// Set VC specific compiler target macros.

// For x86 we have always to prefix by underscore.

// Special case nameless struct/union.

// MinGW-w64 has some additional C99 printf/scanf feature support.
//    So we add some helper macros to ease recognition of them.

// If _FORTIFY_SOURCE is enabled, some inline functions may use
//    __builtin_va_arg_pack().  GCC may report an error if the address
//    of such a function is used.  Set _FORTIFY_VA_ARG=0 in this case.

// Enable workaround for ABI incompatibility on affected platforms

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// http://msdn.microsoft.com/en-us/library/ms175759%28v=VS.100%29.aspx
// Templates won't work in C, will break if secure API is not enabled, disabled

// https://blogs.msdn.com/b/sdl/archive/2010/02/16/vc-2010-and-memcpy.aspx?Redirected=true
// fallback on default implementation if we can't know the size of the destination

// Include _cygwin.h if we're building a Cygwin application.

// Target specific macro replacement for type "long".  In the Windows API,
//    the type long is always 32 bit, even if the target is 64 bit (LLP64).
//    On 64 bit Cygwin, the type long is 64 bit (LP64).  So, to get the right
//    sized definitions and declarations, all usage of type long in the Windows
//    headers have to be replaced by the below defined macro __LONG32.

// C/C++ specific language defines.

// Note the extern. This is needed to work around GCC's
// limitations in handling dllimport attribute.

// Attribute `nonnull' was valid as of gcc 3.3.  We don't use GCC's
//    variadiac macro facility, because variadic macros cause syntax
//    errors with  --traditional-cpp.

//  High byte is the major version, low byte is the minor.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// for backward compatibility

type X__gnuc_va_list = X__builtin_va_list /* vadefs.h:24:29 */

type Ssize_t = int32 /* corecrt.h:52:13 */

type Rsize_t = Size_t /* corecrt.h:57:16 */

type Intptr_t = int32 /* corecrt.h:69:13 */

type Uintptr_t = uint32 /* corecrt.h:82:22 */

type Wint_t = uint16   /* corecrt.h:111:24 */
type Wctype_t = uint16 /* corecrt.h:112:24 */

type Errno_t = int32 /* corecrt.h:118:13 */

type X__time32_t = int32 /* corecrt.h:123:14 */

type X__time64_t = int64 /* corecrt.h:128:35 */

type Time_t = X__time32_t /* corecrt.h:141:20 */

type Threadlocaleinfostruct = struct {
	Frefcount      int32
	Flc_codepage   uint32
	Flc_collate_cp uint32
	Flc_handle     [6]uint32
	Flc_id         [6]LC_ID
	Flc_category   [6]struct {
		Flocale    uintptr
		Fwlocale   uintptr
		Frefcount  uintptr
		Fwrefcount uintptr
	}
	Flc_clike            int32
	Fmb_cur_max          int32
	Flconv_intl_refcount uintptr
	Flconv_num_refcount  uintptr
	Flconv_mon_refcount  uintptr
	Flconv               uintptr
	Fctype1_refcount     uintptr
	Fctype1              uintptr
	Fpctype              uintptr
	Fpclmap              uintptr
	Fpcumap              uintptr
	Flc_time_curr        uintptr
} /* corecrt.h:435:1 */

type Pthreadlocinfo = uintptr /* corecrt.h:437:39 */
type Pthreadmbcinfo = uintptr /* corecrt.h:438:36 */

type Localeinfo_struct = struct {
	Flocinfo Pthreadlocinfo
	Fmbcinfo Pthreadmbcinfo
} /* corecrt.h:441:9 */

type X_locale_tstruct = Localeinfo_struct /* corecrt.h:444:3 */
type X_locale_t = uintptr                 /* corecrt.h:444:19 */

type TagLC_ID = struct {
	FwLanguage uint16
	FwCountry  uint16
	FwCodePage uint16
} /* corecrt.h:435:1 */

type LC_ID = TagLC_ID  /* corecrt.h:452:3 */
type LPLC_ID = uintptr /* corecrt.h:452:9 */

type Threadlocinfo = Threadlocaleinfostruct /* corecrt.h:487:3 */

// Copyright (C) 1992-2020 Free Software Foundation, Inc.
//
// This file is part of GCC.
//
// GCC is free software; you can redistribute it and/or modify it under
// the terms of the GNU General Public License as published by the Free
// Software Foundation; either version 3, or (at your option) any later
// version.
//
// GCC is distributed in the hope that it will be useful, but WITHOUT ANY
// WARRANTY; without even the implied warranty of MERCHANTABILITY or
// FITNESS FOR A PARTICULAR PURPOSE.  See the GNU General Public License
// for more details.
//
// Under Section 7 of GPL version 3, you are granted additional
// permissions described in the GCC Runtime Library Exception, version
// 3.1, as published by the Free Software Foundation.
//
// You should have received a copy of the GNU General Public License and
// a copy of the GCC Runtime Library Exception along with this program;
// see the files COPYING3 and COPYING.RUNTIME respectively.  If not, see
// <http://www.gnu.org/licenses/>.

// This administrivia gets added to the beginning of limits.h
//    if the system has its own version of limits.h.

// We use _GCC_LIMITS_H_ because we want this not to match
//    any macros that the system's limits.h uses for its own purposes.

// Use "..." so that we find syslimits.h only in this same directory.
// syslimits.h stands for the system's own limits.h file.
//    If we can use it ok unmodified, then we install this text.
//    If fixincludes fixes it, then the fixed version is installed
//    instead of this text.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.
// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// File system limits
//
// NOTE: Apparently the actual size of PATH_MAX is 260, but a space is
//       required for the NUL. TODO: Test?
// NOTE: PATH_MAX is the POSIX equivalent for Microsoft's MAX_PATH; the two
//       are semantically identical, with a limit of 259 characters for the
//       path name, plus one for a terminating NUL, for a total of 260.

// Copyright (C) 1991-2020 Free Software Foundation, Inc.
//
// This file is part of GCC.
//
// GCC is free software; you can redistribute it and/or modify it under
// the terms of the GNU General Public License as published by the Free
// Software Foundation; either version 3, or (at your option) any later
// version.
//
// GCC is distributed in the hope that it will be useful, but WITHOUT ANY
// WARRANTY; without even the implied warranty of MERCHANTABILITY or
// FITNESS FOR A PARTICULAR PURPOSE.  See the GNU General Public License
// for more details.
//
// Under Section 7 of GPL version 3, you are granted additional
// permissions described in the GCC Runtime Library Exception, version
// 3.1, as published by the Free Software Foundation.
//
// You should have received a copy of the GNU General Public License and
// a copy of the GCC Runtime Library Exception along with this program;
// see the files COPYING3 and COPYING.RUNTIME respectively.  If not, see
// <http://www.gnu.org/licenses/>.

// Number of bits in a `char'.

// Maximum length of a multibyte character.

// Minimum and maximum values a `signed char' can hold.

// Maximum value an `unsigned char' can hold.  (Minimum is 0).

// Minimum and maximum values a `char' can hold.

// Minimum and maximum values a `signed short int' can hold.

// Maximum value an `unsigned short int' can hold.  (Minimum is 0).

// Minimum and maximum values a `signed int' can hold.

// Maximum value an `unsigned int' can hold.  (Minimum is 0).

// Minimum and maximum values a `signed long int' can hold.
//    (Same as `int').

// Maximum value an `unsigned long int' can hold.  (Minimum is 0).

// Minimum and maximum values a `signed long long int' can hold.

// Maximum value an `unsigned long long int' can hold.  (Minimum is 0).

// Minimum and maximum values a `signed long long int' can hold.

// Maximum value an `unsigned long long int' can hold.  (Minimum is 0).

// This administrivia gets added to the end of limits.h
//    if the system has its own version of limits.h.

type X_onexit_t = uintptr /* stdlib.h:50:15 */

type X_div_t = struct {
	Fquot int32
	Frem  int32
} /* stdlib.h:60:11 */

type Div_t = X_div_t /* stdlib.h:63:5 */

type X_ldiv_t = struct {
	Fquot int32
	Frem  int32
} /* stdlib.h:65:11 */

type Ldiv_t = X_ldiv_t /* stdlib.h:68:5 */

type X_LDOUBLE = struct{ Fld [10]uint8 } /* stdlib.h:77:5 */

type X_CRT_DOUBLE = struct{ Fx float64 } /* stdlib.h:84:5 */

type X_CRT_FLOAT = struct{ Ff float32 } /* stdlib.h:88:5 */

type X_LONGDOUBLE = struct{ Fx float64 } /* stdlib.h:95:5 */

type X_LDBL12 = struct{ Fld12 [12]uint8 } /* stdlib.h:102:5 */

type X_purecall_handler = uintptr /* stdlib.h:143:16 */

type X_invalid_parameter_handler = uintptr /* stdlib.h:148:16 */

type Lldiv_t = struct {
	Fquot int64
	Frem  int64
} /* stdlib.h:727:61 */

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// Return codes for _heapwalk()

// Values for _heapinfo.useflag

// The structure used to walk through the heap with _heapwalk.
type X_heapinfo = struct {
	F_pentry  uintptr
	F_size    Size_t
	F_useflag int32
} /* malloc.h:46:11 */

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// *
// This file has no copyright assigned and is placed in the Public Domain.
// This file is part of the mingw-w64 runtime package.
// No warranty is given; refer to the file DISCLAIMER.PD within this package.

// Return codes for _heapwalk()

// Values for _heapinfo.useflag

// The structure used to walk through the heap with _heapwalk.
type X_HEAPINFO = X_heapinfo /* malloc.h:50:5 */

var _ int8 /* gen.c:2:13: */
