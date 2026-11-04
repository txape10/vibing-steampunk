CLASS zcl_vsp_tadir_move DEFINITION
  PUBLIC
  FINAL
  CREATE PUBLIC.

  PUBLIC SECTION.
    TYPES:
      BEGIN OF ty_move_result,
        success   TYPE abap_bool,
        code      TYPE string,
        message   TYPE string,
        old_pkg   TYPE devclass,
        recording TYPE string,
      END OF ty_move_result.

    "! Move an ABAP object to a different package
    "! @parameter iv_pgmid | Program ID (default: R3TR)
    "! @parameter iv_object | Object type (e.g., CLAS, PROG, INTF)
    "! @parameter iv_obj_name | Object name
    "! @parameter iv_new_pkg | Target package name
    "! @parameter rv_msg | Result message (SUCCESS: or ERROR:)
    CLASS-METHODS move_object
      IMPORTING
        iv_pgmid      TYPE tadir-pgmid DEFAULT 'R3TR'
        iv_object     TYPE tadir-object
        iv_obj_name   TYPE tadir-obj_name
        iv_new_pkg    TYPE devclass
      RETURNING
        VALUE(rv_msg) TYPE string.

    "! Move object; no longer commits itself; the APC handler commits by returning
    "! Use this method when calling from external context
    CLASS-METHODS move_object_and_commit
      IMPORTING
        iv_pgmid      TYPE tadir-pgmid DEFAULT 'R3TR'
        iv_object     TYPE tadir-object
        iv_obj_name   TYPE tadir-obj_name
        iv_new_pkg    TYPE devclass
      RETURNING
        VALUE(rv_msg) TYPE string.

    "! Move a program, class or interface from a local ($) package to a
    "! transportable one and record it in an explicit workbench request.
    "! TADIR is changed first and the object recorded afterwards: the CTS treats an
    "! object of a local package as not recordable. If the recording fails the
    "! package is put back. No COMMIT/ROLLBACK: illegal in an APC handler.
    "! @parameter iv_object | PROG, CLAS or INTF
    "! @parameter iv_obj_name | Object name (up to 30 characters)
    "! @parameter iv_new_pkg | Transportable target package
    "! @parameter iv_request | Workbench request with a modifiable task of the user
    CLASS-METHODS move_object_to_request
      IMPORTING
        iv_object        TYPE tadir-object
        iv_obj_name      TYPE tadir-obj_name
        iv_new_pkg       TYPE devclass
        iv_request       TYPE trkorr
      RETURNING
        VALUE(rs_result) TYPE ty_move_result.

  PRIVATE SECTION.
    CLASS-METHODS fail
      IMPORTING
        iv_code          TYPE string
        iv_message       TYPE string
      RETURNING
        VALUE(rs_result) TYPE ty_move_result.

    CLASS-METHODS check_request
      IMPORTING
        iv_request      TYPE trkorr
      RETURNING
        VALUE(rs_error) TYPE ty_move_result.

    CLASS-METHODS check_lock
      IMPORTING
        iv_object       TYPE tadir-object
        iv_obj_name     TYPE tadir-obj_name
        iv_request      TYPE trkorr
      RETURNING
        VALUE(rs_error) TYPE ty_move_result.

    CLASS-METHODS is_recorded
      IMPORTING
        iv_object          TYPE tadir-object
        iv_obj_name        TYPE tadir-obj_name
        iv_request         TYPE trkorr
      RETURNING
        VALUE(rv_recorded) TYPE abap_bool.

    CLASS-METHODS set_package
      IMPORTING
        is_tadir        TYPE tadir
        iv_pkg          TYPE devclass
        iv_test         TYPE abap_bool
      RETURNING
        VALUE(rv_error) TYPE string.

    CLASS-METHODS record
      IMPORTING
        iv_object       TYPE tadir-object
        iv_obj_name     TYPE tadir-obj_name
        iv_request      TYPE trkorr
      RETURNING
        VALUE(rv_error) TYPE string.

ENDCLASS.

CLASS zcl_vsp_tadir_move IMPLEMENTATION.
  METHOD move_object.
    DATA: ls_tadir     TYPE tadir,
          ls_new_tadir TYPE tadir.

    " Read current TADIR entry
    SELECT SINGLE * FROM tadir INTO ls_tadir
      WHERE pgmid = iv_pgmid
        AND object = iv_object
        AND obj_name = iv_obj_name.

    IF sy-subrc <> 0.
      rv_msg = |ERROR: Object { iv_obj_name } not found in TADIR|.
      RETURN.
    ENDIF.

    " Check if already in target package
    IF ls_tadir-devclass = iv_new_pkg.
      rv_msg = |INFO: Object already in package { iv_new_pkg }|.
      RETURN.
    ENDIF.

    rv_msg = |Old: { ls_tadir-devclass } -> New: { iv_new_pkg }|.

    " Call TR_TADIR_INTERFACE to change package
    CALL FUNCTION 'TR_TADIR_INTERFACE'
      EXPORTING
        wi_test_modus       = space
        wi_tadir_pgmid      = iv_pgmid
        wi_tadir_object     = iv_object
        wi_tadir_obj_name   = iv_obj_name
        wi_tadir_devclass   = iv_new_pkg
        wi_tadir_author     = ls_tadir-author
        wi_tadir_masterlang = ls_tadir-masterlang
      IMPORTING
        new_tadir_entry     = ls_new_tadir
      EXCEPTIONS
        tadir_entry_not_existing       = 1
        tadir_entry_ill_type           = 2
        no_systemname                  = 3
        no_systemtype                  = 4
        original_system_conflict       = 5
        object_reserved_for_devclass   = 6
        object_exists_global           = 7
        object_exists_local            = 8
        object_is_distributed          = 9
        obj_specification_not_unique   = 10
        no_authorization_to_delete     = 11
        devclass_not_existing          = 12
        simultanious_set_remove_repair = 13
        order_missing                  = 14
        no_modification_of_head_syst   = 15
        pgmid_object_not_allowed       = 16
        masterlanguage_not_specified   = 17
        devclass_not_specified         = 18
        specify_owner_unique           = 19
        loc_priv_objs_no_repair        = 20
        gtadir_not_reached             = 21
        object_locked_for_order        = 22
        change_of_class_not_allowed    = 23
        no_change_from_sap_to_tmp      = 24
        OTHERS                         = 25.

    IF sy-subrc = 0.
      rv_msg = |SUCCESS: { rv_msg }|.
    ELSE.
      rv_msg = |ERROR sy-subrc={ sy-subrc } { sy-msgid }-{ sy-msgno }: { sy-msgv1 } { sy-msgv2 }|.
    ENDIF.
  ENDMETHOD.

  METHOD move_object_and_commit.
    rv_msg = move_object(
      iv_pgmid    = iv_pgmid
      iv_object   = iv_object
      iv_obj_name = iv_obj_name
      iv_new_pkg  = iv_new_pkg ).

    " No COMMIT WORK here: it is illegal in an APC handler (APC_ILLEGAL_STATEMENT)
    " and kills the session. The APC framework commits when the handler returns.
  ENDMETHOD.

  METHOD move_object_to_request.
    CONSTANTS lc_pgmid       TYPE tadir-pgmid VALUE 'R3TR'.
    CONSTANTS lc_local_start TYPE c LENGTH 1 VALUE '$'.
    CONSTANTS lc_max_name    TYPE i VALUE 30.
    DATA ls_tadir TYPE tadir.
    DATA lv_error TYPE string.

    IF iv_object <> 'PROG' AND iv_object <> 'CLAS' AND iv_object <> 'INTF'.
      rs_result = fail( iv_code = `OBJECT_TYPE_NOT_SUPPORTED`
                        iv_message = |Object type { iv_object } is not supported; only PROG, CLAS and INTF| ).
      RETURN.
    ENDIF.
    IF strlen( iv_obj_name ) > lc_max_name.
      rs_result = fail( iv_code = `OBJECT_NAME_TOO_LONG`
                        iv_message = |{ iv_obj_name } has more than { lc_max_name } characters; its locks cannot be checked reliably| ).
      RETURN.
    ENDIF.
    IF iv_new_pkg(1) = lc_local_start.
      rs_result = fail( iv_code = `TRANSPORT_NOT_ALLOWED`
                        iv_message = |{ iv_new_pkg } is a local package; no transport request is used there| ).
      RETURN.
    ENDIF.

    SELECT SINGLE * FROM tadir INTO ls_tadir
      WHERE pgmid = lc_pgmid
        AND object = iv_object
        AND obj_name = iv_obj_name.
    IF sy-subrc <> 0.
      rs_result = fail( iv_code = `OBJECT_NOT_FOUND`
                        iv_message = |{ iv_object } { iv_obj_name } not found in TADIR| ).
      RETURN.
    ENDIF.
    IF ls_tadir-srcsystem <> sy-sysid.
      rs_result = fail( iv_code = `ORIGINAL_IN_OTHER_SYSTEM`
                        iv_message = |{ iv_object } { iv_obj_name } has its original in system { ls_tadir-srcsystem }| ).
      RETURN.
    ENDIF.

    " Only a local object moves in; one already in the target package just gets
    " its recording completed (retry after a half-done move)
    IF ls_tadir-devclass <> iv_new_pkg AND ls_tadir-devclass(1) <> lc_local_start.
      rs_result = fail( iv_code = `SOURCE_NOT_LOCAL`
                        iv_message = |{ iv_object } { iv_obj_name } lives in the transportable package { ls_tadir-devclass }; only local objects can be moved in| ).
      RETURN.
    ENDIF.

    DATA(ls_error) = check_request( iv_request ).
    IF ls_error-code IS NOT INITIAL.
      rs_result = ls_error.
      RETURN.
    ENDIF.
    ls_error = check_lock( iv_object = iv_object iv_obj_name = iv_obj_name iv_request = iv_request ).
    IF ls_error-code IS NOT INITIAL.
      rs_result = ls_error.
      RETURN.
    ENDIF.

    rs_result-old_pkg = ls_tadir-devclass.

    IF ls_tadir-devclass = iv_new_pkg.
      IF is_recorded( iv_object = iv_object iv_obj_name = iv_obj_name iv_request = iv_request ) = abap_true.
        rs_result-success = abap_true.
        rs_result-code = `OK`.
        rs_result-recording = `already_recorded`.
        rs_result-message = |{ iv_object } { iv_obj_name } is already in { iv_new_pkg } and recorded in { iv_request }|.
        RETURN.
      ENDIF.
    ELSE.
      " Dry run first: it finds an unknown package, a namespace or delivery
      " unit conflict, etc. without writing anything
      lv_error = set_package( is_tadir = ls_tadir iv_pkg = iv_new_pkg iv_test = abap_true ).
      IF lv_error IS NOT INITIAL.
        rs_result = fail( iv_code = `MOVE_REFUSED` iv_message = lv_error ).
        rs_result-old_pkg = ls_tadir-devclass.
        RETURN.
      ENDIF.
      lv_error = set_package( is_tadir = ls_tadir iv_pkg = iv_new_pkg iv_test = abap_false ).
      IF lv_error IS NOT INITIAL.
        rs_result = fail( iv_code = `MOVE_FAILED` iv_message = lv_error ).
        rs_result-old_pkg = ls_tadir-devclass.
        RETURN.
      ENDIF.
    ENDIF.

    " The object is in the target package now: record it. No rollback exists in
    " an APC handler, so a failure here puts the package back by hand.
    lv_error = record( iv_object = iv_object iv_obj_name = iv_obj_name iv_request = iv_request ).
    IF lv_error IS INITIAL AND is_recorded( iv_object = iv_object iv_obj_name = iv_obj_name iv_request = iv_request ) = abap_false.
      lv_error = |{ iv_object } { iv_obj_name } is not in request { iv_request } after the recording|.
    ENDIF.
    IF lv_error IS NOT INITIAL.
      DATA(lv_undo) = ``.
      IF ls_tadir-devclass <> iv_new_pkg.
        lv_undo = set_package( is_tadir = ls_tadir iv_pkg = ls_tadir-devclass iv_test = abap_false ).
        IF lv_undo IS INITIAL.
          rs_result = fail( iv_code = `RECORD_FAILED_MOVED_BACK`
                            iv_message = |Not recorded, package restored to { ls_tadir-devclass }: { lv_error }| ).
        ELSE.
          rs_result = fail( iv_code = `MOVED_NOT_RECORDED`
                            iv_message = |{ iv_object } { iv_obj_name } is now in { iv_new_pkg } but NOT in request { iv_request }; restoring { ls_tadir-devclass } failed ({ lv_undo }). Repeat the same call to finish the recording. Cause: { lv_error }| ).
        ENDIF.
      ELSE.
        rs_result = fail( iv_code = `MOVED_NOT_RECORDED`
                          iv_message = |{ iv_object } { iv_obj_name } is in { iv_new_pkg } but NOT in request { iv_request }. Repeat the same call. Cause: { lv_error }| ).
      ENDIF.
      rs_result-old_pkg = ls_tadir-devclass.
      RETURN.
    ENDIF.

    SELECT SINGLE devclass FROM tadir INTO @DATA(lv_now)
      WHERE pgmid = @lc_pgmid AND object = @iv_object AND obj_name = @iv_obj_name.
    IF sy-subrc <> 0 OR lv_now <> iv_new_pkg.
      rs_result = fail( iv_code = `VERIFY_FAILED`
                        iv_message = |{ iv_object } { iv_obj_name } was recorded in { iv_request } but TADIR now says { lv_now }; check it in SE03| ).
      rs_result-old_pkg = ls_tadir-devclass.
      RETURN.
    ENDIF.

    rs_result-success = abap_true.
    rs_result-code = `OK`.
    rs_result-old_pkg = ls_tadir-devclass.
    rs_result-recording = `recorded`.
    rs_result-message = |{ iv_object } { iv_obj_name }: { ls_tadir-devclass } -> { iv_new_pkg }, recorded in { iv_request }|.
  ENDMETHOD.

  METHOD fail.
    rs_result = VALUE #( success = abap_false code = iv_code message = iv_message ).
  ENDMETHOD.

  METHOD check_request.
    CONSTANTS lc_workbench    TYPE e070-trfunction VALUE 'K'.
    CONSTANTS lc_modifiable   TYPE e070-trstatus VALUE 'D'.
    CONSTANTS lc_correction   TYPE e070-trfunction VALUE 'S'.
    CONSTANTS lc_unclassified TYPE e070-trfunction VALUE 'X'.
    DATA ls_header TYPE e070.

    IF NOT matches( val = CONV string( iv_request ) pcre = '[A-Z0-9]{3}K[0-9]{6}' ).
      rs_error = fail( iv_code = `TRANSPORT_INVALID` iv_message = |{ iv_request } is not a transport request number| ).
      RETURN.
    ENDIF.
    SELECT SINGLE trfunction, trstatus, strkorr, as4user FROM e070
      WHERE trkorr = @iv_request
      INTO CORRESPONDING FIELDS OF @ls_header.
    IF sy-subrc <> 0.
      rs_error = fail( iv_code = `TRANSPORT_NOT_FOUND` iv_message = |Transport request { iv_request } does not exist| ).
      RETURN.
    ENDIF.
    IF ls_header-strkorr IS NOT INITIAL.
      rs_error = fail( iv_code = `TRANSPORT_IS_TASK` iv_message = |{ iv_request } is a task; give its request { ls_header-strkorr }| ).
      RETURN.
    ENDIF.
    IF ls_header-trfunction <> lc_workbench.
      rs_error = fail( iv_code = `TRANSPORT_INVALID` iv_message = |{ iv_request } is not a workbench request| ).
      RETURN.
    ENDIF.
    IF ls_header-trstatus <> lc_modifiable.
      rs_error = fail( iv_code = `TRANSPORT_NOT_MODIFIABLE` iv_message = |Transport request { iv_request } is not modifiable| ).
      RETURN.
    ENDIF.

    " Without a modifiable task of the user the CTS would open one itself, and its
    " error branches issue ROLLBACK WORK, which is not allowed in an APC handler
    SELECT SINGLE trkorr FROM e070
      WHERE strkorr = @iv_request AND as4user = @sy-uname AND trstatus = @lc_modifiable
        AND ( trfunction = @lc_correction OR trfunction = @lc_unclassified )
      INTO @DATA(lv_own_task).
    IF sy-subrc <> 0.
      IF ls_header-as4user = sy-uname.
        rs_error = fail( iv_code = `TRANSPORT_NO_OWN_TASK`
                         iv_message = |Transport request { iv_request } has no modifiable task of yours; create one in SE09| ).
      ELSE.
        rs_error = fail( iv_code = `TRANSPORT_NOT_OWNED`
                         iv_message = |Transport request { iv_request } belongs to { ls_header-as4user } and you have no task in it| ).
      ENDIF.
    ENDIF.
  ENDMETHOD.

  METHOD check_lock.
    " TLOCK keeps the locks of an object under its type; LOKEY is the name padded
    " to 30 characters, plus a suffix for parts
    DATA(lv_pattern) = replace( val = CONV string( iv_obj_name ) sub = '_' with = '#_' occ = 0 ) && '%'.
    SELECT trkorr, author, lokey FROM tlock
      WHERE object = @iv_object AND lokey LIKE @lv_pattern ESCAPE '#'
      INTO TABLE @DATA(lt_locks).
    LOOP AT lt_locks INTO DATA(ls_lock).
      IF ls_lock-lokey(30) <> iv_obj_name.
        CONTINUE.
      ENDIF.
      DATA(lv_parent) = VALUE e070-strkorr( ).
      SELECT SINGLE strkorr FROM e070 WHERE trkorr = @ls_lock-trkorr INTO @lv_parent.
      DATA(lv_lock_request) = COND trkorr( WHEN lv_parent IS INITIAL THEN ls_lock-trkorr ELSE lv_parent ).
      IF lv_lock_request <> iv_request.
        rs_error = fail( iv_code = `OBJECT_LOCKED_IN_OTHER_REQUEST`
                         iv_message = |{ iv_object } { iv_obj_name } is already in request { lv_lock_request } (user { ls_lock-author })| ).
        RETURN.
      ENDIF.
    ENDLOOP.
  ENDMETHOD.

  METHOD is_recorded.
    SELECT SINGLE e071~trkorr FROM e071
      INNER JOIN e070 ON e070~trkorr = e071~trkorr
      WHERE e071~pgmid = 'R3TR' AND e071~object = @iv_object AND e071~obj_name = @iv_obj_name
        AND ( e070~trkorr = @iv_request OR e070~strkorr = @iv_request )
      INTO @DATA(lv_found).
    rv_recorded = xsdbool( sy-subrc = 0 ).
  ENDMETHOD.

  METHOD set_package.
    DATA ls_new_tadir TYPE tadir.
    DATA lv_test_modus TYPE c LENGTH 1.
    DATA lv_text TYPE string.

    IF iv_test = abap_true.
      lv_test_modus = 'X'.
    ELSE.
      lv_test_modus = space.
    ENDIF.

    CALL FUNCTION 'TR_TADIR_INTERFACE'
      EXPORTING
        wi_test_modus       = lv_test_modus
        wi_tadir_pgmid      = is_tadir-pgmid
        wi_tadir_object     = is_tadir-object
        wi_tadir_obj_name   = is_tadir-obj_name
        wi_tadir_devclass   = iv_pkg
        wi_tadir_author     = is_tadir-author
        wi_tadir_masterlang = is_tadir-masterlang
      IMPORTING
        new_tadir_entry     = ls_new_tadir
      EXCEPTIONS
        tadir_entry_not_existing       = 1
        tadir_entry_ill_type           = 2
        no_systemname                  = 3
        no_systemtype                  = 4
        original_system_conflict       = 5
        object_reserved_for_devclass   = 6
        object_exists_global           = 7
        object_exists_local            = 8
        object_is_distributed          = 9
        obj_specification_not_unique   = 10
        no_authorization_to_delete     = 11
        devclass_not_existing          = 12
        simultanious_set_remove_repair = 13
        order_missing                  = 14
        no_modification_of_head_syst   = 15
        pgmid_object_not_allowed       = 16
        masterlanguage_not_specified   = 17
        devclass_not_specified         = 18
        specify_owner_unique           = 19
        loc_priv_objs_no_repair        = 20
        gtadir_not_reached             = 21
        object_locked_for_order        = 22
        change_of_class_not_allowed    = 23
        no_change_from_sap_to_tmp      = 24
        OTHERS                         = 25.
    IF sy-subrc <> 0.
      IF sy-msgid IS NOT INITIAL.
        MESSAGE ID sy-msgid TYPE 'E' NUMBER sy-msgno WITH sy-msgv1 sy-msgv2 sy-msgv3 sy-msgv4 INTO lv_text.
      ENDIF.
      rv_error = |TR_TADIR_INTERFACE sy-subrc={ sy-subrc } { lv_text }|.
    ENDIF.
  ENDMETHOD.

  METHOD record.
    DATA lt_objects TYPE tredt_objects.
    DATA lv_no_recording TYPE trboolean.
    DATA lv_text TYPE string.

    lt_objects = VALUE #( ( pgmid = 'R3TR' object = iv_object obj_name = iv_obj_name ) ).

    CALL FUNCTION 'TR_RECORD_OBJ_CHANGE_TO_REQ'
      EXPORTING
        iv_request      = iv_request
        it_objects      = lt_objects
      IMPORTING
        ev_no_recording = lv_no_recording
      EXCEPTIONS
        cancel          = 1
        error_message   = 2
        OTHERS          = 3.
    IF sy-subrc <> 0.
      IF sy-msgid IS NOT INITIAL.
        MESSAGE ID sy-msgid TYPE 'E' NUMBER sy-msgno WITH sy-msgv1 sy-msgv2 sy-msgv3 sy-msgv4 INTO lv_text.
      ELSE.
        lv_text = |function ended with return code { sy-subrc } and no message|.
      ENDIF.
      rv_error = |Could not record in request { iv_request }: { lv_text }|.
      RETURN.
    ENDIF.
    IF lv_no_recording IS NOT INITIAL.
      rv_error = |The CTS did not record the object in request { iv_request } (treated it as not recordable)|.
    ENDIF.
  ENDMETHOD.
ENDCLASS.
